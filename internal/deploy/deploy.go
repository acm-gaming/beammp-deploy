package deploy

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/acm-gaming/beammp-deploy/internal/cache"
	"github.com/acm-gaming/beammp-deploy/internal/config"
	"github.com/acm-gaming/beammp-deploy/internal/github"
	"github.com/acm-gaming/beammp-deploy/internal/layout"
	"github.com/acm-gaming/beammp-deploy/internal/remote"
	"go.uber.org/zap"
)

type Deployer struct {
	logger            *zap.Logger
	cfg               *config.Config
	cfgPath           string
	cacheStore        *cache.Store
	githubClient      *github.Client
	observer          Observer
	executedServerCnt int
}

func New(logger *zap.Logger, cfg *config.Config, cfgPath string) (*Deployer, error) {
	store, err := cache.Load("")
	if err != nil {
		return nil, err
	}

	cacheRoot := filepath.Dir(store.Path())
	ghClient := github.NewClient(logger, cacheRoot)

	return &Deployer{
		logger:       logger,
		cfg:          cfg,
		cfgPath:      cfgPath,
		cacheStore:   store,
		githubClient: ghClient,
	}, nil
}

func (d *Deployer) ExecutedServerCount() int {
	return d.executedServerCnt
}

func (d *Deployer) SetObserver(observer Observer) {
	d.observer = observer
}

func (d *Deployer) Run(ctx context.Context, serverFilter []string) error {
	selected, err := d.selectServers(serverFilter)
	if err != nil {
		return err
	}

	totalModules := 0
	for _, server := range selected {
		totalModules += len(server.Modules)
	}
	d.logger.Info("starting deployment", zap.String("config", d.cfgPath), zap.Int("server_count", len(selected)))
	d.emit(Event{
		Type:         EventRunStarted,
		ServerTotal:  len(selected),
		TotalModules: totalModules,
	})

	completedModules := 0

	for serverIndex, server := range selected {
		d.emit(Event{
			Type:             EventServerStarted,
			Server:           server.Name,
			ServerIndex:      serverIndex + 1,
			ServerTotal:      len(selected),
			CompletedModules: completedModules,
			TotalModules:     totalModules,
		})

		if err := d.deployServer(ctx, server, serverIndex+1, len(selected), totalModules, &completedModules); err != nil {
			return err
		}
		d.executedServerCnt++

		d.emit(Event{
			Type:             EventServerCompleted,
			Server:           server.Name,
			ServerIndex:      serverIndex + 1,
			ServerTotal:      len(selected),
			CompletedModules: completedModules,
			TotalModules:     totalModules,
		})
	}

	if err := d.cacheStore.Save(); err != nil {
		return fmt.Errorf("save cache: %w", err)
	}

	d.emit(Event{
		Type:             EventRunCompleted,
		ServerTotal:      len(selected),
		CompletedModules: completedModules,
		TotalModules:     totalModules,
	})
	return nil
}

func (d *Deployer) selectServers(filter []string) ([]config.Server, error) {
	if len(filter) == 0 {
		return d.cfg.Servers, nil
	}

	selected := make([]config.Server, 0, len(filter))
	for _, name := range filter {
		found := false
		for _, server := range d.cfg.Servers {
			if server.Name == name {
				selected = append(selected, server)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("server %q not found in config", name)
		}
	}
	return selected, nil
}

func (d *Deployer) deployServer(ctx context.Context, server config.Server, serverIndex, serverTotal, totalModules int, completedModules *int) error {
	d.logger.Info("connecting server", zap.String("server", server.Name), zap.String("ssh", server.SSH))
	client, err := remote.Connect(server.SSH, server.SSHKey)
	if err != nil {
		return fmt.Errorf("connect to server %s: %w", server.Name, err)
	}
	defer client.Close()

	roots, err := client.FindRoots(server.Path)
	if err != nil {
		return fmt.Errorf("resolve server/client roots for %s: %w", server.Name, err)
	}
	d.logger.Debug("resolved remote roots", zap.String("server", server.Name), zap.String("server_root", roots.Server), zap.String("client_root", roots.Client))
	claims := d.targetClaimsForServer(server.Name)

	for moduleIndex, module := range server.Modules {
		d.emit(Event{
			Type:              EventModuleStarted,
			Server:            server.Name,
			Module:            module.Name,
			ServerIndex:       serverIndex,
			ServerTotal:       serverTotal,
			ServerModuleIndex: moduleIndex + 1,
			ServerModuleTotal: len(server.Modules),
			CompletedModules:  *completedModules,
			TotalModules:      totalModules,
		})

		skipped, err := d.deployModule(ctx, client, server, roots, module, claims)
		if err != nil {
			return err
		}
		(*completedModules)++

		evtType := EventModuleCompleted
		if skipped {
			evtType = EventModuleSkipped
		}
		d.emit(Event{
			Type:              evtType,
			Server:            server.Name,
			Module:            module.Name,
			ServerIndex:       serverIndex,
			ServerTotal:       serverTotal,
			ServerModuleIndex: moduleIndex + 1,
			ServerModuleTotal: len(server.Modules),
			CompletedModules:  *completedModules,
			TotalModules:      totalModules,
		})
	}

	return nil
}

func (d *Deployer) deployModule(ctx context.Context, client *remote.Client, server config.Server, roots *remote.Roots, module config.Module, claims *targetClaims) (bool, error) {
	key := cacheKey(server.Name, module.Name)
	current, hasCurrent := d.cacheStore.Get(key)

	resolved, err := d.githubClient.ResolveModule(ctx, module)
	if err != nil {
		return false, fmt.Errorf("resolve module %s on %s: %w", module.Name, server.Name, err)
	}

	plan, err := layout.Build(module.Name, resolved.SourcePath)
	if err != nil {
		return false, fmt.Errorf("build deploy plan for module %s on %s: %w", module.Name, server.Name, err)
	}

	newTargets := collectTargets(plan, roots)
	if err := claims.Check(module.Name, newTargets, current.RemoteTarget); err != nil {
		return false, fmt.Errorf("detect target collisions for module %s on %s: %w", module.Name, server.Name, err)
	}
	if hasCurrent && !d.requiresDeploy(module, current, resolved, newTargets) {
		d.logger.Info("module unchanged; skipping", zap.String("server", server.Name), zap.String("module", module.Name))
		claims.Update(module.Name, current.RemoteTarget, newTargets)
		return true, nil
	}

	d.logger.Info("deploying module", zap.String("server", server.Name), zap.String("module", module.Name), zap.String("version", resolved.Version), zap.String("commit", resolved.Commit))

	if hasCurrent {
		for _, stale := range current.RemoteTarget {
			if !slices.Contains(newTargets, stale) {
				d.logger.Debug("removing stale module path", zap.String("path", stale))
				if err := client.RemoveDirIfExists(stale); err != nil {
					return false, fmt.Errorf("remove stale path %s: %w", stale, err)
				}
			}
		}
	}

	for _, upload := range plan.Uploads {
		var targetRoot string
		preserveConfig := false
		switch upload.Kind {
		case layout.ServerKind:
			targetRoot = roots.Server
			preserveConfig = true
		case layout.ClientKind:
			targetRoot = roots.Client
		default:
			continue
		}

		remoteTarget := path.Join(targetRoot, upload.RemoteDir)
		if err := client.UploadTree(upload.LocalPath, remoteTarget, preserveConfig); err != nil {
			return false, fmt.Errorf("upload module %s to %s: %w", module.Name, remoteTarget, err)
		}
	}

	d.cacheStore.Set(key, cache.Entry{
		Version:      resolved.Version,
		Commit:       resolved.Commit,
		ServerPath:   roots.Server,
		ClientPath:   roots.Client,
		RemoteTarget: newTargets,
	})
	claims.Update(module.Name, current.RemoteTarget, newTargets)

	d.logger.Info("module deployed", zap.String("server", server.Name), zap.String("module", module.Name))
	return false, nil
}

func (d *Deployer) emit(event Event) {
	if d.observer == nil {
		return
	}
	d.observer.OnDeployEvent(event)
}

func (d *Deployer) requiresDeploy(module config.Module, entry cache.Entry, resolved *github.ResolvedModule, newTargets []string) bool {
	if entry.ServerPath == "" || entry.ClientPath == "" {
		return true
	}
	if !sameStringSet(entry.RemoteTarget, newTargets) {
		return true
	}

	if module.Local != "" || module.Branch != "" {
		return entry.Commit != resolved.Commit
	}

	if entry.Version == "" {
		return true
	}
	currentVer, err1 := normalizeSemver(entry.Version)
	newVer, err2 := normalizeSemver(resolved.Version)
	if err1 == nil && err2 == nil {
		return newVer.GreaterThan(currentVer)
	}
	return strings.TrimSpace(entry.Version) != strings.TrimSpace(resolved.Version)
}

func normalizeSemver(raw string) (*semver.Version, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	return semver.NewVersion(clean)
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func collectTargets(plan *layout.ModulePlan, roots *remote.Roots) []string {
	targets := make([]string, 0, len(plan.Uploads))
	for _, upload := range plan.Uploads {
		switch upload.Kind {
		case layout.ServerKind:
			targets = append(targets, path.Join(roots.Server, upload.RemoteDir))
		case layout.ClientKind:
			targets = append(targets, path.Join(roots.Client, upload.RemoteDir))
		}
	}
	return targets
}

func cacheKey(serverName, moduleName string) string {
	return serverName + "::" + moduleName
}

func (d *Deployer) targetClaimsForServer(serverName string) *targetClaims {
	claims := newTargetClaims()
	prefix := serverName + "::"
	for key, entry := range d.cacheStore.Entries {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		moduleName := strings.TrimPrefix(key, prefix)
		claims.Update(moduleName, nil, entry.RemoteTarget)
	}
	return claims
}

type targetClaims struct {
	mu      sync.Mutex
	owners  map[string]string
	targets map[string]map[string]struct{}
}

func newTargetClaims() *targetClaims {
	return &targetClaims{
		owners:  map[string]string{},
		targets: map[string]map[string]struct{}{},
	}
}

func (c *targetClaims) Check(moduleName string, newTargets []string, previousTargets []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	prevSet := map[string]struct{}{}
	for _, t := range previousTargets {
		prevSet[t] = struct{}{}
	}

	for _, t := range newTargets {
		owner, exists := c.owners[t]
		if !exists || owner == moduleName {
			continue
		}
		if _, previouslyOwned := prevSet[t]; previouslyOwned {
			continue
		}
		return fmt.Errorf("remote target %q for module %q conflicts with module %q", t, moduleName, owner)
	}
	return nil
}

func (c *targetClaims) Update(moduleName string, oldTargets []string, newTargets []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if oldSet, ok := c.targets[moduleName]; ok {
		for old := range oldSet {
			if owner, exists := c.owners[old]; exists && owner == moduleName {
				delete(c.owners, old)
			}
		}
	}

	newSet := map[string]struct{}{}
	for _, t := range newTargets {
		c.owners[t] = moduleName
		newSet[t] = struct{}{}
	}
	c.targets[moduleName] = newSet
}
