package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGetTrackedReposConfiguredAndNonGit(t *testing.T) {
	// Create a temporary configuration directory inside workspace root for safety
	tmpDirRelative, err := os.MkdirTemp(".", "git_test_temp")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		os.RemoveAll(tmpDirRelative)
		t.Fatalf("failed to get absolute path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a dummy config file
	configPath := filepath.Join(tmpDir, "milestone.yml")

	// Create some non-git test directories
	nonGitDir1 := filepath.Join(tmpDir, "nongit1")
	nonGitDir2 := filepath.Join(tmpDir, "nongit2")
	if err := os.MkdirAll(nonGitDir1, 0755); err != nil {
		t.Fatalf("failed to create nongit1: %v", err)
	}
	if err := os.MkdirAll(nonGitDir2, 0755); err != nil {
		t.Fatalf("failed to create nongit2: %v", err)
	}

	// We configure nonGitDir1 and nonGitDir2 relative to root
	rootAbs, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("failed to get root absolute path: %v", err)
	}

	relPath1, err := filepath.Rel(rootAbs, nonGitDir1)
	if err != nil {
		t.Fatalf("failed to get relative path 1: %v", err)
	}
	relPath2, err := filepath.Rel(rootAbs, nonGitDir2)
	if err != nil {
		t.Fatalf("failed to get relative path 2: %v", err)
	}

	// Write configuration yaml
	cfgContent := `
repositories:
  - ` + relPath1 + `
  - ` + relPath2 + `
`
	if err := os.WriteFile(configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write dummy config: %v", err)
	}

	// Override ConfigPath
	oldConfigPath := ConfigPath
	ConfigPath = configPath
	defer func() { ConfigPath = oldConfigPath }()

	// Execute GetTrackedRepos
	repos := GetTrackedRepos()

	// Verify we have root and the two configured nongit repos
	foundRoot := false
	foundNongit1 := false
	foundNongit2 := false

	for _, repo := range repos {
		if repo.Path == "." {
			foundRoot = true
		} else if repo.Path == relPath1 {
			foundNongit1 = true
		} else if repo.Path == relPath2 {
			foundNongit2 = true
		}
	}

	if !foundRoot {
		t.Errorf("expected to find root '.' in tracked repos")
	}
	if !foundNongit1 {
		t.Errorf("expected to find nongit1 relative path %s in tracked repos", relPath1)
	}
	if !foundNongit2 {
		t.Errorf("expected to find nongit2 relative path %s in tracked repos", relPath2)
	}

	// Verify getBranchInDir returns placeholder for the non-git directory
	branch, err := getBranchInDir(nonGitDir1)
	if err != nil {
		t.Errorf("getBranchInDir for non-git path returned error: %v", err)
	}
	if branch != "__not_a_git_worktree__" {
		t.Errorf("expected branch placeholder '__not_a_git_worktree__', got '%s'", branch)
	}

	// Verify CheckoutOrCreateBranchInDir returns nil/no-error for non-git directory
	err = CheckoutOrCreateBranchInDir(nonGitDir1, "test-branch")
	if err != nil {
		t.Errorf("CheckoutOrCreateBranchInDir for non-git path returned error: %v", err)
	}
}

func TestSummarizeRepoStatusReportsBranchDirtyAndMissingWorktree(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	tmpDir, err := os.MkdirTemp("", "git_summary_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	if err := os.WriteFile("changed.txt", []byte("dirty"), 0644); err != nil {
		t.Fatalf("failed to write dirty file: %v", err)
	}

	summary := SummarizeRepoStatus(RepoInfo{Label: "root", Path: "."})
	if !summary.IsWorktree {
		t.Fatal("expected root to be a git worktree")
	}
	if summary.Branch == "" {
		t.Fatal("expected branch to be populated")
	}
	if !summary.Dirty || summary.ChangedCount != 1 {
		t.Fatalf("expected one dirty file, got dirty=%v count=%d status=%q", summary.Dirty, summary.ChangedCount, summary.StatusShort)
	}

	if err := os.Mkdir("plain", 0755); err != nil {
		t.Fatalf("failed to create plain dir: %v", err)
	}
	missing := SummarizeRepoStatus(RepoInfo{Label: "plain", Path: "plain"})
	if missing.IsWorktree {
		t.Fatal("expected plain dir to be reported as non-worktree")
	}
}

func TestGetTrackedReposGitDiscovery(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "git_discovery_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	// Init a git repo
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("failed to run git init: %v", err)
	}
	_ = exec.Command("git", "config", "user.name", "test").Run()
	_ = exec.Command("git", "config", "user.email", "test@test.com").Run()

	// Write dummy commit
	if err := os.WriteFile("dummy.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write dummy file: %v", err)
	}
	_ = exec.Command("git", "add", "dummy.txt").Run()
	_ = exec.Command("git", "commit", "-m", "initial commit").Run()

	// Add worktree
	worktreePath := filepath.Join(tmpDir, "myworktree")
	_ = exec.Command("git", "worktree", "add", worktreePath, "-b", "wt-branch").Run()

	// Write .gitmodules config
	gitmodulesContent := `[submodule "mysubmodule"]
	path = mysubmodule
	url = https://github.com/example/mysubmodule.git
`
	if err := os.WriteFile(".gitmodules", []byte(gitmodulesContent), 0644); err != nil {
		t.Fatalf("failed to write .gitmodules: %v", err)
	}

	oldConfigPath := ConfigPath
	ConfigPath = "nonexistent.yml"
	defer func() { ConfigPath = oldConfigPath }()

	repos := GetTrackedRepos()

	foundSubmodule := false
	foundWorktree := false

	for _, repo := range repos {
		if repo.Path == "mysubmodule" {
			foundSubmodule = true
		}
		if repo.Path == "myworktree" {
			foundWorktree = true
		}
	}

	if !foundSubmodule {
		t.Errorf("expected to find submodule 'mysubmodule' in tracked repos: %v", repos)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil {
		if !foundWorktree {
			t.Errorf("expected to find worktree 'myworktree' in tracked repos: %v", repos)
		}
	}
}

func TestGetTrackedReposNoConfigDiscoversArbitraryReposWithoutLegacyFallback(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "git_no_config_discovery_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)

	if err := os.MkdirAll("packages", 0755); err != nil {
		t.Fatalf("failed to create packages dir: %v", err)
	}
	worktreePath := filepath.Join(tmpDir, "packages", "worker")
	if err := exec.Command("git", "worktree", "add", worktreePath, "-b", "worker-branch").Run(); err != nil {
		t.Fatalf("failed to add worktree: %v", err)
	}

	gitmodulesContent := `[submodule "payments"]
	path = services/payments
	url = https://example.invalid/payments.git
[submodule "escape"]
	path = ../outside
	url = https://example.invalid/outside.git
`
	if err := os.WriteFile(".gitmodules", []byte(gitmodulesContent), 0644); err != nil {
		t.Fatalf("failed to write .gitmodules: %v", err)
	}

	oldConfigPath := ConfigPath
	ConfigPath = "missing-milestone.yml"
	defer func() { ConfigPath = oldConfigPath }()

	repos := GetTrackedRepos()
	got := repoPaths(repos)
	want := []string{".", filepath.Join("packages", "worker"), filepath.Join("services", "payments")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected repos:\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscoverUntrackedEmbeddedReposFindsNestedReposWithoutGitmodules(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "git_embedded_repo_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	if err := os.MkdirAll(filepath.Join("tools", "nested"), 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	if err := exec.Command("git", "-C", filepath.Join("tools", "nested"), "init").Run(); err != nil {
		t.Fatalf("failed to init nested repo: %v", err)
	}

	warnings := DiscoverUntrackedEmbeddedRepos([]RepoInfo{{Label: "root", Path: "."}})
	if len(warnings) != 1 || warnings[0].Path != filepath.Join("tools", "nested") {
		t.Fatalf("unexpected embedded repo warnings: %#v", warnings)
	}
}

func TestDiscoverUntrackedEmbeddedReposSkipsTrackedSubmodules(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "git_embedded_tracked_repo_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	if err := os.MkdirAll(filepath.Join("services", "api"), 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	if err := exec.Command("git", "-C", filepath.Join("services", "api"), "init").Run(); err != nil {
		t.Fatalf("failed to init nested repo: %v", err)
	}

	warnings := DiscoverUntrackedEmbeddedRepos([]RepoInfo{
		{Label: "root", Path: "."},
		{Label: filepath.Join("services", "api"), Path: filepath.Join("services", "api")},
	})
	if len(warnings) != 0 {
		t.Fatalf("expected tracked nested repo to be skipped, got %#v", warnings)
	}
}

func TestDiscoverUntrackedEmbeddedReposFindsRepoInsideTrackedRepo(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "git_embedded_inside_tracked_repo_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	if err := os.MkdirAll(filepath.Join("services", "api", "tools", "nested"), 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	if err := exec.Command("git", "-C", filepath.Join("services", "api"), "init").Run(); err != nil {
		t.Fatalf("failed to init tracked repo: %v", err)
	}
	if err := exec.Command("git", "-C", filepath.Join("services", "api", "tools", "nested"), "init").Run(); err != nil {
		t.Fatalf("failed to init embedded repo: %v", err)
	}

	warnings := DiscoverUntrackedEmbeddedRepos([]RepoInfo{
		{Label: "root", Path: "."},
		{Label: filepath.Join("services", "api"), Path: filepath.Join("services", "api")},
	})
	if len(warnings) != 1 || warnings[0].Path != filepath.Join("services", "api", "tools", "nested") {
		t.Fatalf("expected nested repo inside tracked repo to be warned, got %#v", warnings)
	}
}

func TestDiscoverUntrackedEmbeddedReposSkipsGeneratedVendorDirs(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "git_embedded_generated_repo_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	for _, dir := range []string{
		filepath.Join("node_modules", "pkg"),
		filepath.Join("vendor", "pkg"),
		filepath.Join(".cyclestone", "temp", "repo"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
		if err := exec.Command("git", "-C", dir, "init").Run(); err != nil {
			t.Fatalf("failed to init nested repo %s: %v", dir, err)
		}
	}

	warnings := DiscoverUntrackedEmbeddedRepos([]RepoInfo{{Label: "root", Path: "."}})
	if len(warnings) != 0 {
		t.Fatalf("expected generated/vendor embedded repos to be skipped, got %#v", warnings)
	}
}

func TestGetTrackedReposMergesConfiguredAndDiscoveredDeterministically(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "git_merge_discovery_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)

	if err := os.WriteFile("milestone.yml", []byte(`
repositories:
  - zeta
  - services/payments
  - ../outside
`), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	if err := os.WriteFile(".gitmodules", []byte(`[submodule "payments"]
	path = services/payments
	url = https://example.invalid/payments.git
`), 0644); err != nil {
		t.Fatalf("failed to write .gitmodules: %v", err)
	}

	oldConfigPath := ConfigPath
	ConfigPath = "milestone.yml"
	defer func() { ConfigPath = oldConfigPath }()

	repos := GetTrackedRepos()
	got := repoPaths(repos)
	want := []string{".", filepath.Join("services", "payments"), "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected repos:\n got: %v\nwant: %v", got, want)
	}
}

func TestBranchSnapshotHandlesDetachedHeadAndMissingPath(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "git_branch_snapshot_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	hashBytes, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("failed to get commit hash: %v", err)
	}
	if err := exec.Command("git", "checkout", strings.TrimSpace(string(hashBytes))).Run(); err != nil {
		t.Fatalf("failed to detach HEAD: %v", err)
	}

	snapshot := []RepoBranchSnapshot{
		{Label: "root", Path: ".", Branch: "__detached_head__"},
		{Label: "missing", Path: "missing-dir", Branch: "__not_a_git_worktree__"},
	}
	ok, description := VerifyBranchSnapshot(snapshot)
	if !ok {
		t.Fatalf("expected snapshot verification to pass, got: %s", description)
	}
}

func TestCaptureBranchSnapshotForReposUsesProvidedRepos(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "git_branch_snapshot_for_repos_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	initTestRepo(t)
	if err := os.MkdirAll("configured-extra", 0755); err != nil {
		t.Fatalf("failed to create configured-extra: %v", err)
	}
	if err := os.WriteFile("milestone.yml", []byte(`
repositories:
  - configured-extra
`), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	oldConfigPath := ConfigPath
	ConfigPath = "milestone.yml"
	defer func() { ConfigPath = oldConfigPath }()

	snapshots, err := CaptureBranchSnapshotForRepos([]RepoInfo{
		{Label: "root", Path: "."},
		{Label: "missing", Path: "missing-dir"},
	})
	if err != nil {
		t.Fatalf("CaptureBranchSnapshotForRepos failed: %v", err)
	}
	var hasRoot, hasMissing, hasConfiguredExtra bool
	for _, s := range snapshots {
		if s.Label == "root" && s.Path == "." {
			hasRoot = true
		}
		if s.Label == "missing" && s.Path == "missing-dir" && s.Branch == "__not_a_git_worktree__" {
			hasMissing = true
		}
		if s.Label == "configured-extra" {
			hasConfiguredExtra = true
		}
	}
	if !hasRoot {
		t.Fatalf("expected root entry in snapshot: %v", snapshots)
	}
	if !hasMissing {
		t.Fatalf("expected missing path placeholder in snapshot: %v", snapshots)
	}
	if hasConfiguredExtra {
		t.Fatalf("CaptureBranchSnapshotForRepos rediscovered configured-extra: %v", snapshots)
	}
}

func initTestRepo(t *testing.T) {
	t.Helper()
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("failed to run git init: %v", err)
	}
	_ = exec.Command("git", "config", "user.name", "test").Run()
	_ = exec.Command("git", "config", "user.email", "test@test.com").Run()
	if err := os.WriteFile("dummy.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write dummy file: %v", err)
	}
	if err := exec.Command("git", "add", "dummy.txt").Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}
	if err := exec.Command("git", "commit", "-m", "initial commit").Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}
}

func repoPaths(repos []RepoInfo) []string {
	paths := make([]string, 0, len(repos))
	for _, repo := range repos {
		paths = append(paths, repo.Path)
	}
	return paths
}
