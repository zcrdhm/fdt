const app = getApp();

Page({
  data: {
    step: 'idle', // idle | submitting | pending | done | error
    reqId: '',
    instanceId: '',
    status: '',
    manifestText: '',
    qr2Url: '',
    error: '',
  },

  // Step 1: scan the single-frame QR#1 shown on the intranet Portal.
  scanQR1() {
    this.setData({ step: 'submitting', error: '' });
    tt.scanCode({
      onlyFromCamera: false,
      scanType: ['qrCode'],
      success: (res) => this.submit(res.result),
      fail: (err) => this.setData({ step: 'error', error: '扫码失败: ' + (err.errMsg || '') }),
    });
  },

  // Step 2: hand the signed token to the gateway, which verifies it and creates
  // the native Feishu approval instance.
  submit(frame) {
    if (!frame || frame.indexOf('FDT1.') !== 0) {
      this.setData({ step: 'error', error: '不是有效的摆渡申请二维码' });
      return;
    }
    tt.request({
      url: app.globalData.gatewayBase + '/submit',
      method: 'POST',
      header: { 'content-type': 'application/json' },
      data: { frame },
      success: (r) => {
        if (r.statusCode !== 200) {
          this.setData({ step: 'error', error: '网关校验失败: ' + (r.data || r.statusCode) });
          return;
        }
        this.setData({
          step: 'pending',
          reqId: r.data.reqId,
          instanceId: r.data.instanceId,
          status: r.data.status,
          manifestText: r.data.manifestText,
        });
        this.poll();
      },
      fail: (err) => this.setData({ step: 'error', error: '网络错误: ' + (err.errMsg || '') }),
    });
  },

  // Step 3: poll for approval; when granted, show the QR#2 release image.
  poll() {
    if (this.data.step === 'done') return;
    tt.request({
      url: app.globalData.gatewayBase + '/qr2?reqId=' + encodeURIComponent(this.data.reqId),
      method: 'GET',
      success: (r) => {
        if (r.statusCode === 200 && r.data && r.data.qr2Frame) {
          this.setData({
            step: 'done',
            status: r.data.status,
            qr2Url: app.globalData.gatewayBase + '/qr2/png?reqId=' + encodeURIComponent(this.data.reqId),
          });
          return;
        }
        if (r.statusCode === 200 && r.data) {
          this.setData({ status: r.data.status });
        }
        setTimeout(() => this.poll(), app.globalData.pollIntervalMs);
      },
      fail: () => setTimeout(() => this.poll(), app.globalData.pollIntervalMs),
    });
  },

  reset() {
    this.setData({ step: 'idle', reqId: '', instanceId: '', status: '', qr2Url: '', manifestText: '', error: '' });
  },
});
