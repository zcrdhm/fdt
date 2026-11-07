// Feishu mini-program entry. globalData holds the DMZ gateway base URL — the
// only internet-reachable endpoint this app talks to. It never touches the
// air-gapped intranet directly.
App({
  globalData: {
    // Replace with the deployed DMZ gateway origin.
    gatewayBase: 'https://fdt-gateway.example.com',
    pollIntervalMs: 3000,
  },
});
