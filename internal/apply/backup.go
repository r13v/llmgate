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
	mode := fileMode(target.Sensitive)
	primary := target.Target.Path + ".llmgate.bak"
	if err := writeBackup(fileSystem, primary, target.Original, mode); err == nil {
		return primary, nil
	} else if !isExist(err) {
		return "", err
	}

	timestamped := fmt.Sprintf("%s.llmgate.%s", target.Target.Path, now.UTC().Format("20060102-150405"))
	for index := 0; ; index++ {
		candidate := timestamped + ".bak"
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d.bak", timestamped, index)
		}
		if err := writeBackup(fileSystem, candidate, target.Original, mode); err == nil {
			return candidate, nil
		} else if !isExist(err) {
			return "", err
		}
	}
}

func writeBackup(fileSystem system.FileSystem, path string, content []byte, mode fs.FileMode) error {
	if err := fileSystem.WriteFileExclusive(path, content, mode); err != nil {
		return fmt.Errorf("create backup %s: %w", path, err)
	}
	bestEffortChmod(fileSystem, path, mode)
	return nil
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
