package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/acm-gaming/beammp-deploy/internal/archive"
	"github.com/acm-gaming/beammp-deploy/internal/config"
	"github.com/go-git/go-git/v5"
	gitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogithub "github.com/google/go-github/v67/github"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

type ResolvedModule struct {
	SourcePath string
	Version    string
	Commit     string
}

type Client struct {
	logger     *zap.Logger
	httpClient *http.Client
	api        *gogithub.Client
	cacheDir   string
	token      string
}

func NewClient(logger *zap.Logger, cacheDir string) *Client {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_PAT_TOKEN"))
	}

	httpClient := http.DefaultClient
	if token != "" {
		src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		httpClient = oauth2.NewClient(context.Background(), src)
	}

	return &Client{
		logger:     logger,
		httpClient: httpClient,
		api:        gogithub.NewClient(httpClient),
		cacheDir:   cacheDir,
		token:      token,
	}
}

func (c *Client) ResolveModule(ctx context.Context, module config.Module) (*ResolvedModule, error) {
	if strings.TrimSpace(module.Local) != "" {
		return resolveLocalModule(module)
	}

	owner, repo, err := parseRepository(module.Repository)
	if err != nil {
		return nil, err
	}

	if module.Branch != "" {
		return c.resolveBranchModule(ctx, owner, repo, module)
	}
	return c.resolveReleaseModule(ctx, owner, repo, module.Path)
}

func (c *Client) resolveBranchModule(ctx context.Context, owner, repo string, module config.Module) (*ResolvedModule, error) {
	repoCacheDir := filepath.Join(c.cacheDir, "repos", sanitizePath(owner+"-"+repo))
	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	auth := c.gitAuth()

	repository, err := git.PlainOpen(repoCacheDir)
	if err != nil {
		repository, err = git.PlainCloneContext(ctx, repoCacheDir, false, &git.CloneOptions{
			URL:  repoURL,
			Auth: auth,
		})
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "authentication") {
				return nil, fmt.Errorf("failed to clone %s/%s (auth required, set GITHUB_TOKEN or GITHUB_PAT_TOKEN): %w", owner, repo, err)
			}
			return nil, fmt.Errorf("failed to clone %s/%s: %w", owner, repo, err)
		}
	} else {
		branchRefSpec := gitcfg.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", module.Branch, module.Branch))
		err = repository.FetchContext(ctx, &git.FetchOptions{
			RemoteName: "origin",
			RefSpecs:   []gitcfg.RefSpec{branchRefSpec},
			Force:      true,
			Auth:       auth,
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return nil, fmt.Errorf("failed to fetch updates for %s/%s: %w", owner, repo, err)
		}
	}

	worktree, err := repository.Worktree()
	if err != nil {
		return nil, fmt.Errorf("open worktree for %s/%s: %w", owner, repo, err)
	}

	remoteRefName := plumbing.NewRemoteReferenceName("origin", module.Branch)
	remoteRef, err := repository.Reference(remoteRefName, true)
	if err != nil {
		return nil, fmt.Errorf("branch %q not found in %s/%s", module.Branch, owner, repo)
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Hash: remoteRef.Hash(), Force: true}); err != nil {
		return nil, fmt.Errorf("checkout remote branch %q for %s/%s: %w", module.Branch, owner, repo, err)
	}

	head, err := repository.Head()
	if err != nil {
		return nil, fmt.Errorf("read HEAD for %s/%s: %w", owner, repo, err)
	}

	sourcePath := repoCacheDir
	if module.Path != "" {
		sourcePath = filepath.Join(sourcePath, module.Path)
		info, statErr := os.Stat(sourcePath)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("module path %q not found in branch %s of %s/%s", module.Path, module.Branch, owner, repo)
		}
	}

	return &ResolvedModule{
		SourcePath: sourcePath,
		Commit:     head.Hash().String(),
	}, nil
}

func (c *Client) resolveReleaseModule(ctx context.Context, owner, repo, modulePath string) (*ResolvedModule, error) {
	releases, _, err := c.api.Repositories.ListReleases(ctx, owner, repo, &gogithub.ListOptions{PerPage: 100})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "404") {
			return nil, fmt.Errorf("repository not found: %s/%s", owner, repo)
		}
		return nil, fmt.Errorf("list releases for %s/%s: %w", owner, repo, err)
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("repository %s/%s has no releases and no branch configured", owner, repo)
	}

	chosen := chooseBestRelease(releases)
	if chosen == nil {
		return nil, fmt.Errorf("repository %s/%s has no usable releases", owner, repo)
	}

	version := strings.TrimSpace(chosen.GetTagName())
	if version == "" {
		version = fmt.Sprintf("release-%d", chosen.GetID())
	}

	base := filepath.Join(c.cacheDir, "releases", sanitizePath(owner+"-"+repo), sanitizePath(version))
	extractPath := filepath.Join(base, "extract")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create release cache dir: %w", err)
	}

	if _, err := os.Stat(extractPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("check extract dir: %w", err)
		}

		archivePath, err := c.downloadReleaseArchive(ctx, owner, repo, chosen, base)
		if err != nil {
			return nil, err
		}
		if err := archive.Extract(archivePath, extractPath); err != nil {
			return nil, fmt.Errorf("extract release archive: %w", err)
		}
	}

	root, err := normalizeExtractRoot(extractPath)
	if err != nil {
		return nil, err
	}

	sourcePath := root
	if strings.TrimSpace(modulePath) != "" {
		sourcePath = filepath.Join(root, modulePath)
		info, statErr := os.Stat(sourcePath)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("module path %q not found in release %s of %s/%s", modulePath, version, owner, repo)
		}
	}

	return &ResolvedModule{
		SourcePath: sourcePath,
		Version:    version,
		Commit:     chosen.GetTargetCommitish(),
	}, nil
}

func resolveLocalModule(module config.Module) (*ResolvedModule, error) {
	root := expandHome(strings.TrimSpace(module.Local))
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve local path %q: %w", module.Local, err)
		}
		root = abs
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("local path %q not found: %w", module.Local, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local path %q is not a directory", module.Local)
	}

	sourcePath := root
	if strings.TrimSpace(module.Path) != "" {
		sourcePath = filepath.Join(root, module.Path)
		subInfo, statErr := os.Stat(sourcePath)
		if statErr != nil || !subInfo.IsDir() {
			return nil, fmt.Errorf("module path %q not found under local source %q", module.Path, module.Local)
		}
	}

	digest, err := directoryDigest(sourcePath)
	if err != nil {
		return nil, err
	}

	return &ResolvedModule{
		SourcePath: sourcePath,
		Version:    "local",
		Commit:     "local:" + digest,
	}, nil
}

func directoryDigest(root string) (string, error) {
	h := sha256.New()
	if _, err := io.WriteString(h, "v1\n"); err != nil {
		return "", fmt.Errorf("seed local digest: %w", err)
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			_, _ = io.WriteString(h, "d:"+rel+"\n")
			return nil
		}

		_, _ = io.WriteString(h, "f:"+rel+"\n")
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		_, _ = io.WriteString(h, "\n")
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash local module source %s: %w", root, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func expandHome(v string) string {
	if !strings.HasPrefix(v, "~/") {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return v
	}
	return filepath.Join(home, strings.TrimPrefix(v, "~/"))
}

func (c *Client) downloadReleaseArchive(ctx context.Context, owner, repo string, release *gogithub.RepositoryRelease, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create release archive dir: %w", err)
	}

	target := filepath.Join(dir, "release.zip")
	selectedAsset := findArchiveAsset(release.Assets)
	if selectedAsset != nil {
		if ext := normalizedArchiveExt(selectedAsset.GetName()); ext != "" {
			target = filepath.Join(dir, "release"+ext)
		}

		readCloser, redirectURL, err := c.api.Repositories.DownloadReleaseAsset(ctx, owner, repo, selectedAsset.GetID(), c.httpClient)
		if err != nil {
			return "", fmt.Errorf("download release asset for %s/%s: %w", owner, repo, err)
		}
		if readCloser != nil {
			defer readCloser.Close()
			return target, writeReaderToFile(readCloser, target)
		}
		if ext := normalizedArchiveExt(redirectURL); ext != "" {
			target = filepath.Join(dir, "release"+ext)
		}
		return target, c.downloadURLToFile(ctx, redirectURL, target)
	}

	zipballURL := release.GetZipballURL()
	if strings.TrimSpace(zipballURL) == "" {
		return "", fmt.Errorf("release %s/%s:%s has no downloadable archive", owner, repo, release.GetTagName())
	}

	return target, c.downloadURLToFile(ctx, zipballURL, target)
}

func (c *Client) downloadURLToFile(ctx context.Context, sourceURL, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download archive failed with status %d", resp.StatusCode)
	}

	return writeReaderToFile(resp.Body, targetPath)
}

func writeReaderToFile(r io.Reader, path string) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file %s: %w", path, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func chooseBestRelease(releases []*gogithub.RepositoryRelease) *gogithub.RepositoryRelease {
	type candidate struct {
		release *gogithub.RepositoryRelease
		ver     *semver.Version
	}

	candidates := make([]candidate, 0, len(releases))
	for _, rel := range releases {
		if rel == nil || rel.GetDraft() {
			continue
		}
		tag := strings.TrimSpace(rel.GetTagName())
		tag = strings.TrimPrefix(tag, "v")
		ver, err := semver.NewVersion(tag)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{release: rel, ver: ver})
	}

	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].ver.GreaterThan(candidates[j].ver)
		})
		return candidates[0].release
	}

	for _, rel := range releases {
		if rel != nil && !rel.GetDraft() {
			return rel
		}
	}
	return nil
}

func findArchiveAsset(assets []*gogithub.ReleaseAsset) *gogithub.ReleaseAsset {
	for _, asset := range assets {
		name := strings.ToLower(asset.GetName())
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".tar") {
			return asset
		}
	}
	return nil
}

func normalizeExtractRoot(extractPath string) (string, error) {
	entries, err := os.ReadDir(extractPath)
	if err != nil {
		return "", fmt.Errorf("read extract dir: %w", err)
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(extractPath, entries[0].Name()), nil
	}
	return extractPath, nil
}

func parseRepository(raw string) (owner, repo string, err error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", fmt.Errorf("invalid repository %q; expected one of: https://github.com/org/repo, github.com/org/repo, or org/repo", raw)
	}

	if strings.HasPrefix(value, "git@github.com:") {
		p := strings.TrimPrefix(value, "git@github.com:")
		return parseOwnerRepoPath(raw, p)
	}

	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		const (
			httpPrefix  = "http://"
			httpsPrefix = "https://"
		)
		withoutScheme := value
		if strings.HasPrefix(value, httpsPrefix) {
			withoutScheme = strings.TrimPrefix(value, httpsPrefix)
		} else {
			withoutScheme = strings.TrimPrefix(value, httpPrefix)
		}

		if strings.ContainsAny(withoutScheme, "?#") {
			return "", "", fmt.Errorf("invalid repository %q; query parameters and fragments are not allowed", raw)
		}

		host, remainder, ok := splitHostAndPath(withoutScheme)
		if !ok {
			return "", "", fmt.Errorf("invalid repository %q; expected github.com/owner/repo", raw)
		}
		if !strings.EqualFold(host, "github.com") {
			return "", "", fmt.Errorf("invalid repository %q; only github.com repositories are supported", raw)
		}
		return parseOwnerRepoPath(raw, remainder)
	}

	if host, remainder, ok := splitHostAndPath(value); ok && strings.EqualFold(host, "github.com") {
		return parseOwnerRepoPath(raw, remainder)
	}

	if strings.Contains(value, "://") {
		return "", "", fmt.Errorf("invalid repository %q; only github.com repositories are supported", raw)
	}

	return parseOwnerRepoPath(raw, value)
}

func parseOwnerRepoPath(raw, pathValue string) (string, string, error) {
	clean := strings.Trim(pathValue, "/")
	if clean == "" || strings.ContainsAny(clean, "?#") {
		return "", "", fmt.Errorf("invalid repository %q; expected owner/repo", raw)
	}

	parts := strings.Split(clean, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repository %q; expected exactly owner/repo", raw)
	}

	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(strings.TrimSuffix(parts[1], ".git"))
	if owner == "" || repo == "" || strings.Contains(owner, " ") || strings.Contains(repo, " ") {
		return "", "", fmt.Errorf("invalid repository %q; expected owner/repo", raw)
	}

	return owner, repo, nil
}

func splitHostAndPath(v string) (host string, remainder string, ok bool) {
	idx := strings.IndexByte(v, '/')
	if idx <= 0 || idx >= len(v)-1 {
		return "", "", false
	}
	return v[:idx], v[idx+1:], true
}

func sanitizePath(v string) string {
	v = strings.ToLower(v)
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, " ", "_")
	return v
}

func normalizedArchiveExt(s string) string {
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"):
		return ".tar.gz"
	case strings.HasSuffix(lower, ".tgz"):
		return ".tgz"
	case strings.HasSuffix(lower, ".tar"):
		return ".tar"
	case strings.HasSuffix(lower, ".zip"):
		return ".zip"
	default:
		return ""
	}
}

func (c *Client) gitAuth() *githttp.BasicAuth {
	if c.token == "" {
		return nil
	}
	return &githttp.BasicAuth{
		Username: "x-access-token",
		Password: c.token,
	}
}
