//go:build linux

package atm

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// OpenSerial opens a USB CDC / serial scanner (8N1, raw) at path.
// baud is the line speed; 9600 is typical for barcode scanners.
func OpenSerial(path string, baud int) (*SerialScanner, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := configureTermios(int(f.Fd()), baud); err != nil {
		f.Close()
		return nil, err
	}
	return NewSerialScanner(f), nil
}

func configureTermios(fd, baud int) error {
	term, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("atm: termios: %w", err)
	}
	term.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	term.Oflag &^= unix.OPOST
	term.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	term.Cflag &^= unix.CSIZE | unix.PARENB
	term.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	speed, err := baudConst(baud)
	if err != nil {
		return err
	}
	term.Ispeed = speed
	term.Ospeed = speed
	return unix.IoctlSetTermios(fd, unix.TCSETS, term)
}

func baudConst(baud int) (uint32, error) {
	switch baud {
	case 0, 9600:
		return unix.B9600, nil
	case 19200:
		return unix.B19200, nil
	case 38400:
		return unix.B38400, nil
	case 57600:
		return unix.B57600, nil
	case 115200:
		return unix.B115200, nil
	default:
		return 0, fmt.Errorf("atm: unsupported baud %d", baud)
	}
}
