package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// Git branch resolution for the status bar. HEAD is read directly instead of
// shelling out to git, so this stays cheap enough to run on every tick.

// currentGitBranch returns the checked-out branch (or short commit when
// detached) for the directory ru was launched in, or "" when not in a git
// repository. It reads .git/HEAD directly to avoid spawning a subprocess.
func currentGitBranch() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return gitBranchIn(dir)
}

// focusedBranch resolves the git branch for the focused pane's working
// directory (reported via OSC 7), falling back to the launch directory.
func (m model) focusedBranch() string {
	if m.shell != nil {
		if cwd := m.shell.getCwd(); cwd != "" {
			return gitBranchIn(cwd)
		}
	}
	return currentGitBranch()
}

func gitBranchIn(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	return parseGitHead(strings.TrimSpace(string(data)))
}

func parseGitHead(head string) string {
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(head) >= 7 {
		return head[:7] // detached HEAD: short SHA
	}
	return head
}

// findGitDir walks up from start looking for a .git directory or file,
// resolving the "gitdir:" pointer used by worktrees and submodules.
func findGitDir(start string) string {
	dir := start
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return gitPath
			}
			data, readErr := os.ReadFile(gitPath)
			if readErr == nil {
				if gd, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
					if !filepath.IsAbs(gd) {
						gd = filepath.Join(dir, gd)
					}
					return gd
				}
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
