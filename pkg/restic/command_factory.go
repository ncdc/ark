package restic

import (
	"fmt"
	"strings"
)

// BackupCommand returns a Command for running a restic backup.
func BackupCommand(repoPrefix, repo, passwordFile, path string, tags map[string]string) *Command {
	return &Command{
		Command:      "backup",
		RepoPrefix:   repoPrefix,
		Repo:         repo,
		PasswordFile: passwordFile,
		Args:         []string{path},
		ExtraFlags:   backupTagFlags(tags),
	}
}

func backupTagFlags(tags map[string]string) []string {
	var flags []string
	for k, v := range tags {
		flags = append(flags, fmt.Sprintf("--tag=%s=%s", k, v))
	}
	return flags
}

// RestoreCommand returns a Command for running a restic restore.
func RestoreCommand(repoPrefix, repo, passwordFile, podUID, snapshotID string) *Command {
	return &Command{
		Command:      "restore",
		RepoPrefix:   repoPrefix,
		Repo:         repo,
		PasswordFile: passwordFile,
		Args:         []string{snapshotID},
		ExtraFlags:   []string{fmt.Sprintf("--target=/restores/%s", podUID)},
	}
}

// GetSnapshotCommand returns a Command for running a restic (get) snapshots.
func GetSnapshotCommand(repoPrefix, repo, passwordFile string, tags map[string]string) *Command {
	return &Command{
		Command:      "snapshots",
		RepoPrefix:   repoPrefix,
		Repo:         repo,
		PasswordFile: passwordFile,
		ExtraFlags:   []string{"--json", "--last", getSnapshotTagFlag(tags)},
	}
}

func getSnapshotTagFlag(tags map[string]string) string {
	var tagFilters []string
	for k, v := range tags {
		tagFilters = append(tagFilters, fmt.Sprintf("%s=%s", k, v))
	}

	return fmt.Sprintf("--tag=%s", strings.Join(tagFilters, ","))
}
