package skills

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kayiik/compa/pkg/config"
)

// GlobalDir returns the folder of the skills every workspace's agent sees:
// skills in Compa's home. The agent, the web API, evolution and the CLI all
// use it and BuiltinDir, so they agree on which skills there are.
func GlobalDir() string {
	return filepath.Join(config.GetHome(), "skills")
}

// BuiltinDir returns the directory built-in skills are loaded from:
// $COMPA_BUILTIN_SKILLS when set, otherwise a skills folder installed next to
// the executable, otherwise "" (none). The bundled default skills reach the
// agent through onboarding, which copies them into the workspace.
//
// It never falls back to the working directory: launching Compa from a folder
// that happens to contain skills/ must not add those skills to the system
// prompt as built-ins.
func BuiltinDir() string {
	if dir := strings.TrimSpace(os.Getenv(config.EnvBuiltinSkills)); dir != "" {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return installedBuiltinDir(filepath.Dir(exe))
}

// installedBuiltinDir is the install-location rule, split from the process
// lookup so a test can reach it.
func installedBuiltinDir(exeDir string) string {
	if exeDir == "" {
		return ""
	}
	dir := filepath.Join(exeDir, "skills")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}
