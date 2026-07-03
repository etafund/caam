package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/config"
	"github.com/charmbracelet/lipgloss"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	orig, ok := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, orig)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestThemeOptionsFromEnv_NoColor(t *testing.T) {
	unsetEnv(t, "CAAM_TUI_THEME")
	unsetEnv(t, "CAAM_TUI_CONTRAST")
	unsetEnv(t, "TERM")
	t.Setenv("NO_COLOR", "1")

	opts := ThemeOptionsFromEnv()
	if !opts.NoColor {
		t.Fatal("expected NoColor when NO_COLOR is set")
	}
}

func TestThemeOptionsFromEnv_TermDumbNoColor(t *testing.T) {
	unsetEnv(t, "NO_COLOR")
	unsetEnv(t, "CAAM_TUI_THEME")
	unsetEnv(t, "CAAM_TUI_CONTRAST")
	t.Setenv("TERM", "dumb")

	opts := ThemeOptionsFromEnv()
	if !opts.NoColor {
		t.Fatal("expected NoColor when TERM=dumb")
	}
	t.Logf("theme env fallback term=%q no_color=%t mode=%s contrast=%s", os.Getenv("TERM"), opts.NoColor, opts.Mode, opts.Contrast)
}

func TestThemeOptionsFromEnv_Overrides(t *testing.T) {
	unsetEnv(t, "NO_COLOR")
	unsetEnv(t, "TERM")
	unsetEnv(t, "CAAM_TUI_REDUCED_MOTION")
	unsetEnv(t, "CAAM_REDUCED_MOTION")
	unsetEnv(t, "REDUCED_MOTION")
	t.Setenv("CAAM_TUI_THEME", "light")
	t.Setenv("CAAM_TUI_CONTRAST", "high")

	opts := ThemeOptionsFromEnv()
	if opts.Mode != ThemeLight {
		t.Fatalf("expected Mode=light, got %q", opts.Mode)
	}
	if opts.Contrast != ContrastHigh {
		t.Fatalf("expected Contrast=high, got %q", opts.Contrast)
	}
	if opts.NoColor {
		t.Fatal("expected NoColor=false when NO_COLOR not set")
	}
}

func TestThemeOptionsFromEnv_ReducedMotion(t *testing.T) {
	unsetEnv(t, "NO_COLOR")
	unsetEnv(t, "TERM")
	unsetEnv(t, "CAAM_TUI_THEME")
	unsetEnv(t, "CAAM_TUI_CONTRAST")
	t.Setenv("CAAM_TUI_REDUCED_MOTION", "1")

	opts := ThemeOptionsFromEnv()
	if !opts.ReducedMotion {
		t.Fatal("expected ReducedMotion=true when CAAM_TUI_REDUCED_MOTION is set")
	}
}

func TestThemeOptionsFromConfig_Fixtures(t *testing.T) {
	unsetEnv(t, "NO_COLOR")
	unsetEnv(t, "TERM")

	cfg := config.DefaultSPMConfig()
	cfg.TUI.Theme = "dark"
	cfg.TUI.HighContrast = true
	cfg.TUI.ReducedMotion = true
	cfg.TUI.Toasts = false
	cfg.TUI.Mouse = false
	cfg.TUI.ShowKeyHints = false
	cfg.TUI.Density = "compact"
	cfg.TUI.NoTUI = true

	prefs := TUIPreferencesFromConfig(cfg)
	if prefs.Mode != ThemeDark {
		t.Fatalf("expected config Mode=dark, got %q", prefs.Mode)
	}
	if prefs.Contrast != ContrastHigh {
		t.Fatalf("expected config Contrast=high, got %q", prefs.Contrast)
	}
	if !prefs.ReducedMotion {
		t.Fatal("expected reduced motion from config")
	}
	if prefs.Toasts {
		t.Fatal("expected toasts disabled from config")
	}
	if prefs.Mouse {
		t.Fatal("expected mouse disabled from config")
	}
	if prefs.ShowKeyHints {
		t.Fatal("expected key hints disabled from config")
	}
	if prefs.Density != "compact" {
		t.Fatalf("expected compact density, got %q", prefs.Density)
	}
	if !prefs.NoTUI {
		t.Fatal("expected no_tui enabled from config")
	}
	t.Logf("tui prefs source=config mode=%s contrast=%s reduced_motion=%t toasts=%t mouse=%t key_hints=%t density=%s no_tui=%t",
		prefs.Mode, prefs.Contrast, prefs.ReducedMotion, prefs.Toasts, prefs.Mouse, prefs.ShowKeyHints, prefs.Density, prefs.NoTUI)
}

func TestThemeOptionsFromConfig_NoColorEnvOverride(t *testing.T) {
	unsetEnv(t, "TERM")
	t.Setenv("NO_COLOR", "1")

	cfg := config.DefaultSPMConfig()
	cfg.TUI.Theme = "light"

	opts := ThemeOptionsFromConfig(cfg)
	if opts.Mode != ThemeLight {
		t.Fatalf("expected config Mode=light, got %q", opts.Mode)
	}
	if !opts.NoColor {
		t.Fatal("expected NO_COLOR env override to force no-color rendering")
	}
	t.Logf("theme config with env override mode=%s no_color=%t", opts.Mode, opts.NoColor)
}

func TestNewTheme_ModeSelection(t *testing.T) {
	light := NewTheme(ThemeOptions{Mode: ThemeLight, Contrast: ContrastNormal})
	if _, ok := light.Palette.Text.(lipgloss.Color); !ok {
		t.Fatalf("expected Color for light mode, got %T", light.Palette.Text)
	}

	auto := NewTheme(ThemeOptions{Mode: ThemeAuto, Contrast: ContrastNormal})
	if _, ok := auto.Palette.Text.(lipgloss.AdaptiveColor); !ok {
		t.Fatalf("expected AdaptiveColor for auto mode, got %T", auto.Palette.Text)
	}
}

func TestNewTheme_NoColor(t *testing.T) {
	theme := NewTheme(ThemeOptions{Mode: ThemeAuto, Contrast: ContrastNormal, NoColor: true})
	if _, ok := theme.Palette.Text.(lipgloss.NoColor); !ok {
		t.Fatalf("expected NoColor palette, got %T", theme.Palette.Text)
	}
	if theme.Border != lipgloss.HiddenBorder() {
		t.Fatalf("expected hidden border for no-color theme")
	}
}

func TestNewTheme_HighContrastTokensAreLegible(t *testing.T) {
	tests := []ThemeMode{ThemeLight, ThemeDark}
	for _, mode := range tests {
		t.Run(string(mode), func(t *testing.T) {
			theme := NewTheme(ThemeOptions{Mode: mode, Contrast: ContrastHigh})
			if theme.Contrast != ContrastHigh {
				t.Fatalf("expected high contrast theme, got %q", theme.Contrast)
			}

			assertDistinctToken(t, "text/background", theme.Palette.Text, theme.Palette.Background)
			assertDistinctToken(t, "text/surface", theme.Palette.Text, theme.Palette.Surface)
			assertDistinctToken(t, "keycap text/bg", theme.Palette.KeycapText, theme.Palette.KeycapBg)
			assertDistinctToken(t, "danger/surface", theme.Palette.Danger, theme.Palette.Surface)
			assertDistinctToken(t, "accent/surface", theme.Palette.Accent, theme.Palette.Surface)

			t.Logf("high contrast mode=%s text=%s background=%s accent=%s danger=%s keycap_text=%s keycap_bg=%s",
				mode,
				colorTokenKey(theme.Palette.Text),
				colorTokenKey(theme.Palette.Background),
				colorTokenKey(theme.Palette.Accent),
				colorTokenKey(theme.Palette.Danger),
				colorTokenKey(theme.Palette.KeycapText),
				colorTokenKey(theme.Palette.KeycapBg))
		})
	}
}

func TestNewStyles_CriticalStylesRender(t *testing.T) {
	theme := NewTheme(ThemeOptions{Mode: ThemeDark, Contrast: ContrastHigh})
	styles := NewStyles(theme)

	tests := []struct {
		name   string
		render string
	}{
		{name: "status success", render: styles.StatusSuccess.Render("success")},
		{name: "status warning", render: styles.StatusWarning.Render("warning")},
		{name: "status error", render: styles.StatusError.Render("error")},
		{name: "dialog focused", render: styles.DialogFocused.Render("dialog")},
		{name: "field required", render: styles.FieldRequired.Render("*")},
		{name: "field error", render: styles.FieldError.Render("required")},
		{name: "search prompt", render: styles.SearchPrompt.Render("/")},
		{name: "toast info", render: styles.ToastInfo.Render("info")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.render == "" {
				t.Fatal("critical style rendered empty output")
			}
			t.Logf("critical style=%q width=%d", tt.name, lipgloss.Width(tt.render))
		})
	}
}

func assertDistinctToken(t *testing.T, label string, a, b lipgloss.TerminalColor) {
	t.Helper()
	if colorTokenKey(a) == colorTokenKey(b) {
		t.Fatalf("%s tokens should be distinct, both are %s", label, colorTokenKey(a))
	}
}

func colorTokenKey(color lipgloss.TerminalColor) string {
	return fmt.Sprintf("%T:%v", color, color)
}
