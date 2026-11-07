package atm

import (
	"fmt"
	"strings"

	"fdt/internal/model"
)

// FAT/vfat 单文件上限为 4GiB-1；exFAT/NTFS/ext* 不受此限。
const fatMaxFileBytes int64 = (4 << 30) - 1

// usbWriteHeadroom leaves a little free space for directories / FS metadata.
const usbWriteHeadroom int64 = 1 << 20 // 1 MiB

// ErrFilesystemLimit means the USB FS cannot hold one or more files (e.g. FAT + >4GB).
// Token must NOT be consumed — operator can swap media and retry the same QR#2.
var ErrFilesystemLimit = fmt.Errorf("atm: U盘文件系统不支持本次数据")

func isFATLimitedFS(fs string) bool {
	switch strings.ToLower(strings.TrimSpace(fs)) {
	case "vfat", "msdos", "fat", "fat12", "fat16", "fat32":
		return true
	default:
		return false
	}
}

func fmtBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// checkUSBForExport validates free space and FS file-size limits before Claim/write.
// On error the QR#2 must remain valid (caller must not Consume).
func checkUSBForExport(dev Device, files []model.FileEntry) (totalSize int64, fileNames []string, err error) {
	fileNames = make([]string, 0, len(files))
	var largest model.FileEntry
	for _, f := range files {
		totalSize += f.Size
		if f.Path != "" {
			fileNames = append(fileNames, f.Path)
		}
		if f.Size > largest.Size {
			largest = f
		}
	}
	need := totalSize + usbWriteHeadroom
	free := dev.FreeBytes()
	if free < need {
		return totalSize, fileNames, fmt.Errorf(
			"%w：需要约 %s（含少量预留），当前剩余 %s（文件系统 %s）。QR#2 未核销，请清理或更换更大容量的 U 盘后重新扫码兑付",
			ErrInsufficientSpace, fmtBytes(need), fmtBytes(free), fsLabel(dev.FileSystem()))
	}
	if isFATLimitedFS(dev.FileSystem()) && largest.Size > fatMaxFileBytes {
		name := largest.Path
		if name == "" {
			name = "(未命名文件)"
		}
		return totalSize, fileNames, fmt.Errorf(
			"%w：当前为 %s，单文件不能超过 4GB；「%s」约 %s。QR#2 未核销，请格式化为 exFAT/NTFS 或更换 U 盘后重新扫码兑付",
			ErrFilesystemLimit, fsLabel(dev.FileSystem()), name, fmtBytes(largest.Size))
	}
	return totalSize, fileNames, nil
}

func fsLabel(fs string) string {
	fs = strings.TrimSpace(fs)
	if fs == "" {
		return "未知"
	}
	return fs
}
