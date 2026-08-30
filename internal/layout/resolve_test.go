package layout

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// useTempConfigDir points os.UserConfigDir at a temporary directory
// and returns it. Every resolution test calls it: the global config is
// a rung of the chain, and a test must never consult the real one.
//
// The variable that has to be overridden differs per platform; where
// it cannot be overridden reliably, the test skips rather than reads
// the user's own configuration.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", home)
	case "darwin", "ios":
		t.Setenv("HOME", home)
	case "plan9":
		t.Setenv("home", home)
	default:
		t.Setenv("XDG_CONFIG_HOME", home)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("cannot override the user config directory on %s: %v", runtime.GOOS, err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Skipf("user config directory %q is not under the override %q on %s", dir, home, runtime.GOOS)
	}
	return dir
}

// writeGlobalConfig puts content in the (already overridden) global
// config file.
func writeGlobalConfig(t *testing.T, configDir, content string) {
	t.Helper()
	dir := filepath.Join(configDir, ConfigDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveChain(t *testing.T) {
	tests := []struct {
		name string
		// build lays out the tree and returns the destination.
		build        func(t *testing.T, root, configDir string) string
		flag         string
		wantPattern  string
		wantSource   func(dest string) string
		wantDeclared bool
	}{
		{
			name: "flag beats the destination marker",
			build: func(t *testing.T, root, _ string) string {
				return writeMarker(t, mkdir(t, root, "dest"), "layout = \"Capture\"\n")
			},
			flag:         "{yyyy-mm}",
			wantPattern:  "{yyyy-mm}",
			wantSource:   func(string) string { return SourceFlag },
			wantDeclared: true,
		},
		{
			name: "flag beats an inherited container layout",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{yyyy}\"\n")
				return mkdir(t, root, "dest")
			},
			flag:         "Capture",
			wantPattern:  "Capture",
			wantSource:   func(string) string { return SourceFlag },
			wantDeclared: true,
		},
		{
			name: "the destination's own marker beats a container",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{yyyy}\"\n")
				return writeMarker(t, mkdir(t, root, "dest"), "layout = \"{yyyy-mm-dd}\"\n")
			},
			wantPattern:  "{yyyy-mm-dd}",
			wantSource:   func(dest string) string { return filepath.Join(dest, MarkerName) },
			wantDeclared: true,
		},
		{
			name: "a marker with both keys is an archive, not a container",
			build: func(t *testing.T, root, _ string) string {
				return writeMarker(t, mkdir(t, root, "dest"),
					"layout = \"Capture\"\nlayout-for-children = \"{yyyy}\"\n")
			},
			wantPattern:  "Capture",
			wantSource:   func(dest string) string { return filepath.Join(dest, MarkerName) },
			wantDeclared: true,
		},
		{
			name: "the destination's own flat layout is declared",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{yyyy}\"\n")
				return writeMarker(t, mkdir(t, root, "dest"), "layout = \"\"\n")
			},
			wantPattern:  "",
			wantSource:   func(dest string) string { return filepath.Join(dest, MarkerName) },
			wantDeclared: true,
		},
		{
			name: "a container beats the global config",
			build: func(t *testing.T, root, configDir string) string {
				writeGlobalConfig(t, configDir, "layout = \"{yyyy-mm-dd}\"\n")
				writeMarker(t, root, "layout-for-children = \"{yyyy}\"\n")
				return mkdir(t, root, "dest")
			},
			wantPattern: "{yyyy}",
			wantSource:  func(dest string) string { return filepath.Join(filepath.Dir(dest), MarkerName) },
		},
		{
			name: "the nearest container wins",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{yyyy-mm-dd}\"\n")
				near := writeMarker(t, mkdir(t, root, "near"), "layout-for-children = \"{yyyy}\"\n")
				return mkdir(t, near, "dest")
			},
			wantPattern: "{yyyy}",
			wantSource:  func(dest string) string { return filepath.Join(filepath.Dir(dest), MarkerName) },
		},
		{
			name: "a container several levels up still reaches",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{yyyy-mm}\"\n")
				return mkdir(t, root, "a", "b", "c", "dest")
			},
			wantPattern: "{yyyy-mm}",
			wantSource:  func(dest string) string { return filepath.Join(dest, "..", "..", "..", "..", MarkerName) },
		},
		{
			name: "an inherited flat layout is not declared",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"\"\n")
				return mkdir(t, root, "dest")
			},
			wantPattern: "",
			wantSource:  func(dest string) string { return filepath.Join(filepath.Dir(dest), MarkerName) },
		},
		{
			name: "an intervening archive root stops inheritance",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{yyyy-mm-dd}\"\n")
				archive := writeMarker(t, mkdir(t, root, "archive"), "layout = \"Capture\"\n")
				return mkdir(t, archive, "sub")
			},
			wantPattern: DefaultPattern,
			wantSource:  func(string) string { return SourceDefault },
		},
		{
			name: "the global config beats the default",
			build: func(t *testing.T, root, configDir string) string {
				writeGlobalConfig(t, configDir, "# mine\nlayout = \"{yyyy}/{mm}\"\n")
				return mkdir(t, root, "dest")
			},
			wantPattern: "{yyyy}/{mm}",
			wantSource:  func(string) string { return SourceConfig },
		},
		{
			name: "a flat global config is honored",
			build: func(t *testing.T, root, configDir string) string {
				writeGlobalConfig(t, configDir, "layout = \"\"\n")
				return mkdir(t, root, "dest")
			},
			wantPattern: "",
			wantSource:  func(string) string { return SourceConfig },
		},
		{
			name: "a config without a layout key falls through",
			build: func(t *testing.T, root, configDir string) string {
				writeGlobalConfig(t, configDir, "# nothing set\n")
				return mkdir(t, root, "dest")
			},
			wantPattern: DefaultPattern,
			wantSource:  func(string) string { return SourceDefault },
		},
		{
			name: "the built-in default is the last rung",
			build: func(t *testing.T, root, _ string) string {
				return mkdir(t, root, "dest")
			},
			wantPattern: DefaultPattern,
			wantSource:  func(string) string { return SourceDefault },
		},
		{
			name: "a marker with only a dam key declares no layout",
			build: func(t *testing.T, root, _ string) string {
				return writeMarker(t, mkdir(t, root, "dest"), "dam = \"lrc\"\n")
			},
			wantPattern: DefaultPattern,
			wantSource:  func(string) string { return SourceDefault },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := useTempConfigDir(t)
			dest := tt.build(t, t.TempDir(), configDir)

			res, err := Resolve(dest, tt.flag)
			if err != nil {
				t.Fatalf("Resolve = %v, want no error", err)
			}
			if got := res.Pattern.String(); got != tt.wantPattern {
				t.Errorf("Pattern = %q, want %q", got, tt.wantPattern)
			}
			if want := filepath.Clean(tt.wantSource(dest)); res.Source != want {
				t.Errorf("Source = %q, want %q", res.Source, want)
			}
			if res.Declared != tt.wantDeclared {
				t.Errorf("Declared = %v, want %v", res.Declared, tt.wantDeclared)
			}
			wantPath := res.Source // a marker rung reports its own path
			switch res.Source {
			case SourceFlag, SourceDefault:
				wantPath = ""
			case SourceConfig:
				wantPath = filepath.Join(configDir, ConfigDirName, ConfigFileName)
			}
			if res.SourcePath != wantPath {
				t.Errorf("SourcePath = %q, want %q", res.SourcePath, wantPath)
			}
		})
	}
}

func TestResolveReportsTheDestinationMarker(t *testing.T) {
	useTempConfigDir(t)
	root := t.TempDir()
	writeMarker(t, root, "layout-for-children = \"{yyyy-mm}\"\n")
	dest := writeMarker(t, mkdir(t, root, "dest"), "dam = \"lrc\"\ncolour = \"blue\"\n")

	res, err := Resolve(dest, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Marker == nil {
		t.Fatal("Marker = nil, want the destination's own marker")
	}
	if res.Marker.DAM != "lrc" {
		t.Errorf("Marker.DAM = %q, want %q", res.Marker.DAM, "lrc")
	}
	// The inherited layout won, and the marker is still reported.
	if got, want := res.Pattern.String(), "{yyyy-mm}"; got != want {
		t.Errorf("Pattern = %q, want %q", got, want)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "colour") {
		t.Errorf("Warnings = %v, want one about the unknown key", res.Warnings)
	}
}

func TestResolveCollectsWarningsFromEveryFileConsulted(t *testing.T) {
	configDir := useTempConfigDir(t)
	writeGlobalConfig(t, configDir, "layout = \"{yyyy}\"\nverbose = \"yes\"\n")
	root := t.TempDir()
	writeMarker(t, root, "stray line\n")
	dest := writeMarker(t, mkdir(t, root, "dest"), "unknown-key = \"1\"\n")

	res, err := Resolve(dest, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceConfig {
		t.Fatalf("Source = %q, want %q", res.Source, SourceConfig)
	}
	if len(res.Warnings) != 3 {
		t.Fatalf("Warnings = %v, want 3 (destination marker, ancestor marker, global config)", res.Warnings)
	}
	for _, want := range []string{"unknown-key", "stray line", "verbose"} {
		found := false
		for _, w := range res.Warnings {
			if strings.Contains(w, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("Warnings = %v, want one mentioning %q", res.Warnings, want)
		}
	}
}

func TestResolveContainerIsRefused(t *testing.T) {
	tests := []struct {
		name string
		flag string
	}{
		{"without a flag", ""},
		{"even with an explicit layout", "{yyyy}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTempConfigDir(t)
			dest := writeMarker(t, t.TempDir(), "layout-for-children = \"{yyyy}/{mm}\"\n")

			res, err := Resolve(dest, tt.flag)
			if !errors.Is(err, ErrContainer) {
				t.Fatalf("Resolve = %v, want ErrContainer", err)
			}
			var containerErr *ContainerError
			if !errors.As(err, &containerErr) {
				t.Fatalf("Resolve = %v, want a *ContainerError", err)
			}
			if got, want := containerErr.Marker.Path(), filepath.Join(dest, MarkerName); got != want {
				t.Errorf("ContainerError marker = %q, want %q", got, want)
			}
			if got, want := containerErr.Marker.LayoutForChildren, "{yyyy}/{mm}"; got != want {
				t.Errorf("LayoutForChildren = %q, want %q", got, want)
			}
			if !strings.Contains(err.Error(), dest) {
				t.Errorf("error = %q, want it to name the container", err)
			}
			if res.Marker == nil {
				t.Error("Marker = nil, want the container's marker reported alongside the error")
			}
		})
	}
}

func TestResolveRejectsBadLayouts(t *testing.T) {
	tests := []struct {
		name    string
		build   func(t *testing.T, root, configDir string) string
		flag    string
		wantErr string
	}{
		{
			name:    "an impossible flag",
			build:   func(t *testing.T, root, _ string) string { return mkdir(t, root, "dest") },
			flag:    "{shoot}",
			wantErr: SourceFlag,
		},
		{
			name: "a marker layout that does not parse",
			build: func(t *testing.T, root, _ string) string {
				return writeMarker(t, mkdir(t, root, "dest"), "layout = \"../escape\"\n")
			},
			wantErr: MarkerName,
		},
		{
			name: "a container layout that does not parse",
			build: func(t *testing.T, root, _ string) string {
				writeMarker(t, root, "layout-for-children = \"{nope}\"\n")
				return mkdir(t, root, "dest")
			},
			wantErr: KeyLayoutForChildren,
		},
		{
			name: "a global config layout that does not parse",
			build: func(t *testing.T, root, configDir string) string {
				writeGlobalConfig(t, configDir, "layout = \"/absolute\"\n")
				return mkdir(t, root, "dest")
			},
			wantErr: ConfigFileName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := useTempConfigDir(t)
			dest := tt.build(t, t.TempDir(), configDir)
			_, err := Resolve(dest, tt.flag)
			if err == nil {
				t.Fatal("Resolve = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "invalid layout") {
				t.Errorf("error = %q, want it to explain the layout", err)
			}
		})
	}
}

func TestResolveFlagDistinguishesFlatFromUnset(t *testing.T) {
	useTempConfigDir(t)
	dest := writeMarker(t, t.TempDir(), "layout = \"{yyyy}\"\n")

	flat := ""
	res, err := ResolveFlag(dest, &flat)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pattern.IsFlat() {
		t.Errorf("Pattern = %q, want the flat layout", res.Pattern)
	}
	if res.Source != SourceFlag || !res.Declared {
		t.Errorf("Source = %q, Declared = %v; want %q, true", res.Source, res.Declared, SourceFlag)
	}

	res, err = ResolveFlag(dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Pattern.String(), "{yyyy}"; got != want {
		t.Errorf("Pattern = %q, want the marker's %q", got, want)
	}
}

func TestResolveAcceptsARelativeDestination(t *testing.T) {
	useTempConfigDir(t)
	root := t.TempDir()
	writeMarker(t, mkdir(t, root, "dest"), "layout = \"Capture\"\n")
	t.Chdir(root)

	res, err := Resolve("dest", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Pattern.String(), "Capture"; got != want {
		t.Errorf("Pattern = %q, want %q", got, want)
	}
	// Provenance is absolute whatever the caller passed, so a report
	// names one unambiguous file.
	if !filepath.IsAbs(res.Source) {
		t.Errorf("Source = %q, want an absolute marker path", res.Source)
	}
}

func TestResolveMissingDestination(t *testing.T) {
	useTempConfigDir(t)
	root := t.TempDir()
	writeMarker(t, root, "layout-for-children = \"{yyyy}\"\n")

	// A destination that does not exist yet still resolves: the CLI
	// previews before it creates.
	res, err := Resolve(filepath.Join(root, "not-yet"), "")
	if err != nil {
		t.Fatalf("Resolve = %v, want no error", err)
	}
	if got, want := res.Pattern.String(), "{yyyy}"; got != want {
		t.Errorf("Pattern = %q, want %q", got, want)
	}
	if res.Marker != nil {
		t.Errorf("Marker = %+v, want nil", res.Marker)
	}
}

func TestNearestRoot(t *testing.T) {
	t.Run("includes the directory itself", func(t *testing.T) {
		dir := writeMarker(t, t.TempDir(), "layout = \"{yyyy}\"\n")
		m, err := NearestRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		if m == nil {
			t.Fatal("NearestRoot = nil, want the directory's own marker")
		}
		if got, want := m.Path(), filepath.Join(dir, MarkerName); got != want {
			t.Errorf("Path() = %q, want %q", got, want)
		}
	})

	t.Run("finds the innermost ancestor", func(t *testing.T) {
		root := t.TempDir()
		writeMarker(t, root, "layout = \"{yyyy-mm}\"\n")
		inner := writeMarker(t, mkdir(t, root, "inner"), "layout = \"{yyyy}\"\n")
		deep := mkdir(t, inner, "a", "b")

		m, err := NearestRoot(deep)
		if err != nil {
			t.Fatal(err)
		}
		if m == nil || m.Dir != inner {
			t.Fatalf("NearestRoot = %v, want the marker at %q", m, inner)
		}
	})

	t.Run("walks past containers", func(t *testing.T) {
		root := t.TempDir()
		archive := writeMarker(t, mkdir(t, root, "archive"), "layout = \"{yyyy}\"\n")
		container := writeMarker(t, mkdir(t, archive, "container"), "layout-for-children = \"{mm}\"\n")

		m, err := NearestRoot(mkdir(t, container, "sub"))
		if err != nil {
			t.Fatal(err)
		}
		if m == nil || m.Dir != archive {
			t.Fatalf("NearestRoot = %v, want the archive at %q — a container is not a root", m, archive)
		}
	})

	t.Run("none", func(t *testing.T) {
		m, err := NearestRoot(mkdir(t, t.TempDir(), "a", "b"))
		if err != nil {
			t.Fatal(err)
		}
		if m != nil {
			t.Errorf("NearestRoot = %+v, want nil outside any archive", m)
		}
	})

	t.Run("stops at the filesystem root", func(t *testing.T) {
		abs, err := filepath.Abs(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		// The volume root is its own parent on every platform, so the
		// walk ends there rather than spinning. (A hang here fails the
		// package by timeout rather than by assertion.)
		fsRoot := filepath.VolumeName(abs) + string(filepath.Separator)
		if got := parentDir(fsRoot); got != "" {
			t.Errorf("parentDir(%q) = %q, want %q", fsRoot, got, "")
		}
		if _, err := NearestRoot(fsRoot); err != nil {
			t.Errorf("NearestRoot(%q) = %v", fsRoot, err)
		}
	})

	t.Run("reports an unreadable marker", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, MarkerName), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := NearestRoot(dir); err == nil {
			t.Error("NearestRoot = nil error, want the unreadable marker reported")
		}
	})
}

func TestParentDir(t *testing.T) {
	tests := []struct {
		dir  string
		want string
	}{
		{filepath.Join("a", "b", "c"), filepath.Join("a", "b")},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests,
			struct{ dir, want string }{`C:\a\b`, `C:\a`},
			struct{ dir, want string }{`C:\a`, `C:\`},
			struct{ dir, want string }{`C:\`, ""},
			struct{ dir, want string }{`\\server\share`, ""},
		)
	} else {
		tests = append(tests,
			struct{ dir, want string }{"/a/b", "/a"},
			struct{ dir, want string }{"/a", "/"},
			struct{ dir, want string }{"/", ""},
		)
	}

	for _, tt := range tests {
		if got := parentDir(tt.dir); got != tt.want {
			t.Errorf("parentDir(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestUpwardWalkTerminates(t *testing.T) {
	abs, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	steps := 0
	for dir := abs; dir != ""; dir = parentDir(dir) {
		steps++
		if steps > 256 {
			t.Fatalf("walking up from %q did not terminate", abs)
		}
	}
}

func TestConfigPath(t *testing.T) {
	configDir := useTempConfigDir(t)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(configDir, ConfigDirName, ConfigFileName); got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}
