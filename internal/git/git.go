package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"
)

// runGit runs a git command and returns its output.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git error: %v, stderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GetCurrentBranch returns the name of the active Git branch.
func GetCurrentBranch() (string, error) {
	return runGit("rev-parse", "--abbrev-ref", "HEAD")
}

// CheckoutBranch performs a Git checkout for the specified branch.
func CheckoutBranch(branch string) (string, error) {
	return runGit("checkout", branch)
}

// CreateBranch creates and switches to a new Git branch.
func CreateBranch(branch string) (string, error) {
	return runGit("checkout", "-b", branch)
}

// Commit creates a new Git commit with the given message.
func Commit(message string) (string, error) {
	// Let's allow empty commit or regular commits. Let's do a regular commit.
	// Note: We might want to pass --allow-empty if there are no changes, just to mock milestone progression.
	return runGit("commit", "--allow-empty", "-m", message)
}

// GetDiff retrieves the current git diff of unstaged and staged changes.
func GetDiff() (string, error) {
	// Returns diff of staged + unstaged changes
	unstaged, err := runGit("diff")
	if err != nil {
		return "", err
	}
	staged, err := runGit("diff", "--cached")
	if err != nil {
		return "", err
	}

	sep := ""
	if unstaged != "" && staged != "" {
		sep = "\n\n"
	}
	return staged + sep + unstaged, nil
}

// GetLatestCommitHash returns the short hash of the latest commit on the current branch.
func GetLatestCommitHash() (string, error) {
	return runGit("rev-parse", "--short", "HEAD")
}

// IsGitRepository checks if the current working directory is a git repository.
func IsGitRepository() bool {
	_, err := runGit("rev-parse", "--is-inside-work-tree")
	return err == nil
}

// CheckoutOrCreateBranch performs a checkout or creation of a branch in the root directory.
func CheckoutOrCreateBranch(branchName string) error {
	return CheckoutOrCreateBranchInDir(".", branchName)
}

// CheckoutOrCreateBranchInDir checks out or creates a branch in a specific directory.
func CheckoutOrCreateBranchInDir(dir, branchName string) error {
	if !isGitWorktree(dir) {
		return nil
	}
	cmdVerify := exec.Command("git", "-C", dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	if cmdVerify.Run() == nil {
		cmdCheckout := exec.Command("git", "-C", dir, "checkout", branchName)
		if err := cmdCheckout.Run(); err != nil {
			return fmt.Errorf("checkout existing branch %s failed in %s: %w", branchName, dir, err)
		}
	} else {
		cmdCreate := exec.Command("git", "-C", dir, "checkout", "-b", branchName)
		if err := cmdCreate.Run(); err != nil {
			return fmt.Errorf("create branch %s failed in %s: %w", branchName, dir, err)
		}
	}
	return nil
}

// RepoInfo represents a git repository worktree.
type RepoInfo struct {
	Label string
	Path  string
}

// RepoStatusSummary is a read-only snapshot of one tracked repository for
// preflight review and other non-mutating checks.
type RepoStatusSummary struct {
	Label        string
	Path         string
	IsWorktree   bool
	Branch       string
	Detached     bool
	Unknown      bool
	Dirty        bool
	ChangedCount int
	StatusShort  string
}

// EmbeddedRepoWarning describes a nested git repository that is not part of
// Cyclestone's configured/discovered tracked repository set.
type EmbeddedRepoWarning struct {
	Path string
}

// ConfigPath is the configurable path to the milestone configuration.
// Milestones are now loaded from the folder-per-item .cyclestone/milestones/
// directory; this path is used only for the optional repositories list.
var ConfigPath = ".cyclestone/milestone.yml"

// GetTrackedRepos returns all configured, worktree-discovered, and submodule git/non-git directories.
func GetTrackedRepos() []RepoInfo {
	var repos []RepoInfo

	rootAbs, err := filepath.Abs(".")
	if err != nil {
		repos = append(repos, RepoInfo{Label: "root", Path: "."})
		return repos
	}

	repos = append(repos, RepoInfo{Label: "root", Path: "."})
	seen := map[string]bool{".": true}

	var candidates []string

	// 1. Configured repositories
	cfg, err := config.LoadConfig(ConfigPath)
	var configRepos []string
	if err == nil && cfg != nil {
		configRepos = cfg.Repositories
	}

	candidates = append(candidates, configRepos...)

	// 2. Root-level submodules
	submodules := getSubmodules(".")
	candidates = append(candidates, submodules...)

	// 3. Git worktrees
	worktrees, _ := getGitWorktrees()
	candidates = append(candidates, worktrees...)

	// Clean and deduplicate candidates relative to root
	var uniqueRelativePaths []string
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		cleanRel, err := cleanRelativePath(rootAbs, cand)
		if err != nil {
			continue
		}
		if cleanRel == "." || isOutOfRootRelativePath(cleanRel) {
			continue
		}
		if !seen[cleanRel] {
			seen[cleanRel] = true
			uniqueRelativePaths = append(uniqueRelativePaths, cleanRel)
		}
	}

	// Sort relative paths alphabetically for deterministic order
	sort.Strings(uniqueRelativePaths)

	// Append deduplicated and sorted paths
	for _, relPath := range uniqueRelativePaths {
		repos = append(repos, RepoInfo{
			Label: relPath,
			Path:  relPath,
		})
	}

	return repos
}

// DiscoverUntrackedEmbeddedRepos returns nested git repository roots that are
// inside the current repository but absent from the tracked repository list.
func DiscoverUntrackedEmbeddedRepos(repos []RepoInfo) []EmbeddedRepoWarning {
	rootAbs, err := filepath.Abs(".")
	if err != nil {
		return nil
	}

	tracked := map[string]bool{}
	for _, repo := range repos {
		cleanRel, err := cleanRelativePath(rootAbs, repo.Path)
		if err != nil || cleanRel == "" {
			continue
		}
		tracked[cleanRel] = true
	}

	var warnings []EmbeddedRepoWarning
	seen := map[string]bool{}
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		cleanPath := filepath.Clean(path)
		if cleanPath == "." {
			return nil
		}

		if entry.IsDir() {
			name := entry.Name()
			if shouldSkipEmbeddedRepoScanDir(name) {
				return filepath.SkipDir
			}
			if name == ".git" {
				if parent := filepath.Dir(cleanPath); parent != "." {
					addEmbeddedRepoWarning(parent, tracked, seen, &warnings)
				}
				return filepath.SkipDir
			}
			return nil
		}

		if entry.Name() == ".git" {
			if parent := filepath.Dir(cleanPath); parent != "." {
				addEmbeddedRepoWarning(parent, tracked, seen, &warnings)
			}
		}
		return nil
	})

	sort.Slice(warnings, func(i, j int) bool {
		return warnings[i].Path < warnings[j].Path
	})
	return warnings
}

func addEmbeddedRepoWarning(path string, tracked, seen map[string]bool, warnings *[]EmbeddedRepoWarning) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "." || tracked[cleanPath] || seen[cleanPath] {
		return
	}
	seen[cleanPath] = true
	*warnings = append(*warnings, EmbeddedRepoWarning{Path: cleanPath})
}

func shouldSkipEmbeddedRepoScanDir(name string) bool {
	switch name {
	case ".cyclestone", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func cleanRelativePath(rootAbs, p string) (string, error) {
	var absPath string
	if filepath.IsAbs(p) {
		absPath = filepath.Clean(p)
	} else {
		absPath = filepath.Join(rootAbs, p)
	}

	rel, err := filepath.Rel(rootAbs, absPath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(rel), nil
}

func isOutOfRootRelativePath(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

func getGitWorktrees() ([]string, error) {
	out, err := runGit("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var worktrees []string
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			worktrees = append(worktrees, strings.TrimSpace(path))
		}
	}
	return worktrees, nil
}

func isGitWorktree(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	topLevel, err := filepath.Abs(strings.TrimSpace(string(out)))
	if err != nil {
		return false
	}
	return filepath.Clean(topLevel) == filepath.Clean(absDir)
}

// IsGitWorktree reports whether dir is the top-level directory of a git worktree.
func IsGitWorktree(dir string) bool {
	return isGitWorktree(dir)
}

// SummarizeRepoStatus returns current branch and dirty-state information for a
// tracked repository without modifying branches, files, or index state.
func SummarizeRepoStatus(repo RepoInfo) RepoStatusSummary {
	summary := RepoStatusSummary{
		Label: repo.Label,
		Path:  repo.Path,
	}
	if !isGitWorktree(repo.Path) {
		return summary
	}
	summary.IsWorktree = true

	cmd := exec.Command("git", "-C", repo.Path, "rev-parse", "--abbrev-ref", "HEAD")
	var branchOut bytes.Buffer
	cmd.Stdout = &branchOut
	if err := cmd.Run(); err != nil {
		summary.Unknown = true
	} else {
		branch := strings.TrimSpace(branchOut.String())
		switch branch {
		case "":
			summary.Unknown = true
		case "HEAD":
			summary.Detached = true
			if hash, err := exec.Command("git", "-C", repo.Path, "rev-parse", "--short", "HEAD").Output(); err == nil {
				summary.Branch = strings.TrimSpace(string(hash))
			}
		default:
			summary.Branch = branch
		}
	}

	statusCmd := exec.Command("git", "-C", repo.Path, "status", "--short")
	var statusOut bytes.Buffer
	statusCmd.Stdout = &statusOut
	if err := statusCmd.Run(); err == nil {
		status := strings.TrimRight(statusOut.String(), "\n")
		summary.StatusShort = status
		if strings.TrimSpace(status) != "" {
			summary.Dirty = true
			for _, line := range strings.Split(status, "\n") {
				if strings.TrimSpace(line) != "" {
					summary.ChangedCount++
				}
			}
		}
	}

	return summary
}

// SummarizeTrackedRepoStatuses summarizes all repositories returned by
// GetTrackedRepos. It is intentionally bounded to tracked discovery results.
func SummarizeTrackedRepoStatuses() []RepoStatusSummary {
	repos := GetTrackedRepos()
	summaries := make([]RepoStatusSummary, 0, len(repos))
	for _, repo := range repos {
		summaries = append(summaries, SummarizeRepoStatus(repo))
	}
	return summaries
}

func getSubmodules(parentDir string) []string {
	gitmodulesPath := filepath.Join(parentDir, ".gitmodules")
	if _, err := os.Stat(gitmodulesPath); err != nil {
		return nil
	}
	cmd := exec.Command("git", "config", "-f", gitmodulesPath, "--get-regexp", `^submodule\..*\.path$`)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}

	var submodules []string
	lines := strings.Split(out.String(), "\n")
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && strings.TrimSpace(key) != "" {
			submodules = append(submodules, strings.TrimSpace(value))
		}
	}
	return submodules
}

func getBranchInDir(dir string) (string, error) {
	if !isGitWorktree(dir) {
		return "__not_a_git_worktree__", nil
	}
	cmd := exec.Command("git", "-C", dir, "branch", "--show-current")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		cmd2 := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
		var out2 bytes.Buffer
		cmd2.Stdout = &out2
		if err2 := cmd2.Run(); err2 == nil {
			b := strings.TrimSpace(out2.String())
			if b == "HEAD" {
				return "__detached_head__", nil
			}
			return b, nil
		}
		return "__not_a_git_worktree__", err
	}
	b := strings.TrimSpace(out.String())
	if b == "" {
		return "__detached_head__", nil
	}
	return b, nil
}

// RepoBranchSnapshot represents the captured branch state of a repository.
type RepoBranchSnapshot struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// CaptureBranchSnapshot captures the current branch for all tracked repositories/submodules.
func CaptureBranchSnapshot() ([]RepoBranchSnapshot, error) {
	repos := GetTrackedRepos()
	return CaptureBranchSnapshotForRepos(repos)
}

// CaptureBranchSnapshotForRepos captures the current branch for the provided repositories/submodules.
func CaptureBranchSnapshotForRepos(repos []RepoInfo) ([]RepoBranchSnapshot, error) {
	var snapshots []RepoBranchSnapshot
	for _, repo := range repos {
		branch, err := getBranchInDir(repo.Path)
		if err != nil {
			branch = "__unknown__"
		}
		snapshots = append(snapshots, RepoBranchSnapshot{
			Label:  repo.Label,
			Path:   repo.Path,
			Branch: branch,
		})
	}
	return snapshots, nil
}

// VerifyBranchSnapshot compares the current branches of all tracked repos against a saved snapshot.
// Returns true if matches, or false with violation descriptions.
func VerifyBranchSnapshot(snapshots []RepoBranchSnapshot) (bool, string) {
	var violations []string
	for _, snap := range snapshots {
		currentBranch, err := getBranchInDir(snap.Path)
		if err != nil {
			currentBranch = "__unknown__"
		}

		if currentBranch != snap.Branch {
			violations = append(violations, fmt.Sprintf("%s changed branch from %s to %s", snap.Label, snap.Branch, currentBranch))
		}
	}

	if len(violations) > 0 {
		return false, strings.Join(violations, "; ")
	}
	return true, ""
}
