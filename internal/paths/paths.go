package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/crueber/cuisle/internal/apperr"
)

// Resolve computes the directories for env without touching the filesystem.
//
//	darwin:  Data ~/Library/Application Support/Cuisle   Logs ~/Library/Logs/Cuisle
//	windows: Data %LOCALAPPDATA%\Cuisle                  Logs %LOCALAPPDATA%\Cuisle\logs
//	linux:   Data ${XDG_DATA_HOME:-~/.local/share}/cuisle Logs ${XDG_STATE_HOME:-~/.local/state}/cuisle
//
// CUISLE_DATA_DIR=<d> sets Data=<d>, Logs=<d>/logs.
func Resolve(env Env) (Paths, error) {
	if d := env.Getenv(EnvDataDir); d != "" {
		return Paths{Data: d, Logs: filepath.Join(d, "logs")}, nil
	}
	home := func() (string, error) {
		if env.Home == "" {
			return "", apperr.New(apperr.Internal, "cannot determine the home directory")
		}
		return env.Home, nil
	}
	xdg := func(name, fallback string) (string, error) {
		if v := env.Getenv(name); v != "" {
			return v, nil
		}
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, fallback), nil
	}

	switch env.GOOS {
	case "darwin":
		h, err := home()
		if err != nil {
			return Paths{}, err
		}
		return Paths{
			Data: filepath.Join(h, "Library", "Application Support", "Cuisle"),
			Logs: filepath.Join(h, "Library", "Logs", "Cuisle"),
		}, nil
	case "windows":
		local := env.Getenv("LOCALAPPDATA")
		if local == "" {
			h, err := home()
			if err != nil {
				return Paths{}, err
			}
			local = filepath.Join(h, "AppData", "Local")
		}
		data := filepath.Join(local, "Cuisle")
		return Paths{Data: data, Logs: filepath.Join(data, "logs")}, nil
	default:
		dataBase, err := xdg("XDG_DATA_HOME", filepath.Join(".local", "share"))
		if err != nil {
			return Paths{}, err
		}
		stateBase, err := xdg("XDG_STATE_HOME", filepath.Join(".local", "state"))
		if err != nil {
			return Paths{}, err
		}
		return Paths{
			Data: filepath.Join(dataBase, "cuisle"),
			Logs: filepath.Join(stateBase, "cuisle"),
		}, nil
	}
}

// Default resolves for the running process and creates both directories (0700).
func Default() (Paths, error) {
	home, _ := os.UserHomeDir() // empty home is reported by Resolve only when it is needed
	p, err := Resolve(Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv})
	if err != nil {
		return Paths{}, err
	}
	for _, dir := range []string{p.Data, p.Logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Paths{}, apperr.Wrap(apperr.Internal, "cannot create "+dir, fmt.Errorf("mkdir %s: %w", dir, err))
		}
	}
	return p, nil
}

// DB returns the database path.
func (p Paths) DB() string { return filepath.Join(p.Data, DBFile) }
