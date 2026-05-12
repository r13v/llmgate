package apply

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/r13v/llmgate/internal/system"
)

const (
	sensitiveFileMode fs.FileMode = 0o600
	regularFileMode   fs.FileMode = 0o644
	sensitiveDirMode  fs.FileMode = 0o700
)

func createBackup(fileSystem system.FileSystem, target TargetPlan, now time.Time) (string, error) {
	if !target.OriginalExists {
		return "", nil
	}
	backupPath, err := chooseBackupPath(fileSystem, target.Target.Path, now)
	if err != nil {
		return "", err
	}
	mode := fileMode(target.Sensitive)
	if err := fileSystem.WriteFile(backupPath, target.Original, mode); err != nil {
		return "", fmt.Errorf("write backup %s: %w", backupPath, err)
	}
	bestEffortChmod(fileSystem, backupPath, mode)
	return backupPath, nil
}

func chooseBackupPath(fileSystem system.FileSystem, path string, now time.Time) (string, error) {
	primary := path + ".llmgate.bak"
	_, err := fileSystem.Stat(primary)
	if err != nil {
		if isNotExist(err) {
			return primary, nil
		}
		return "", fmt.Errorf("check backup path %s: %w", primary, err)
	}
	return fmt.Sprintf("%s.llmgate.%s.bak", path, now.UTC().Format("20060102-150405")), nil
}

func fileMode(sensitive bool) fs.FileMode {
	if sensitive {
		return sensitiveFileMode
	}
	return regularFileMode
}

func bestEffortChmod(fileSystem system.FileSystem, path string, mode fs.FileMode) {
	_ = fileSystem.Chmod(path, mode)
}
