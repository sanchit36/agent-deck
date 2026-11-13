package session

// GroupProjectPaths returns the paths a group's GitHub Projects resolve from
// (spec #18 §2): the ProjectPath of each session directly in groupPath (not
// in its child groups), never AdditionalPaths, with a worktree session
// standing for its main repository (WorktreeRepoRoot). Each path appears
// once, in session order. It reads only the instances, never the disk.
func GroupProjectPaths(groupPath string, instances []*Instance) []string {
	var paths []string
	seen := map[string]bool{}
	for _, inst := range instances {
		if inst == nil || inst.GroupPath != groupPath {
			continue
		}
		p := ProjectResolvePath(inst)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths
}

// ProjectResolvePath is the path inst's GitHub Project resolves from: its
// ProjectPath, or for a worktree session its main repository
// (WorktreeRepoRoot). Empty for a nil instance or one with no path.
func ProjectResolvePath(inst *Instance) string {
	if inst == nil {
		return ""
	}
	if inst.IsWorktree() && inst.WorktreeRepoRoot != "" {
		return inst.WorktreeRepoRoot
	}
	return inst.ProjectPath
}
