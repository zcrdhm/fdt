package atm

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// DecodeQRImage decodes the first QR code found in img and returns it only if
// SanitizeScan accepts it as an FDT frame. Non-FDT codes are ignored.
func DecodeQRImage(img image.Image) (string, error) {
	if img == nil {
		return "", fmt.Errorf("atm: nil image")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	hints := map[gozxing.DecodeHintType]interface{}{
		gozxing.DecodeHintType_TRY_HARDER:       true,
		gozxing.DecodeHintType_POSSIBLE_FORMATS: []gozxing.BarcodeFormat{gozxing.BarcodeFormat_QR_CODE},
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		return "", err
	}
	frame, ok := SanitizeScan(res.GetText())
	if !ok {
		return "", fmt.Errorf("atm: QR content is not an FDT frame")
	}
	return frame, nil
}

// DecodeQRBytes decodes a PNG/JPEG still image.
func DecodeQRBytes(data []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return DecodeQRImage(img)
}

// DecodeQRFile reads a still image from path and decodes an FDT QR frame.
func DecodeQRFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return DecodeQRBytes(b)
}
