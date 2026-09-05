//go:build linux || darwin

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkar/gtach"
)

func TestPromptShells(t *testing.T) {
	for _, shell := range []string{"bash", "sh", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " not installed")
			}
			dir, err := os.MkdirTemp("", "gt-prompt-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			home := filepath.Join(dir, "home")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("ENV", filepath.Join(home, ".shrc"))
			t.Setenv("ZDOTDIR", home)
			t.Setenv("PS1", "user> ")
			t.Setenv("TERM", "dumb")
			t.Setenv("GTACH", "outside")
			for name, contents := range map[string]string{
				".bashrc": `PS1='user> '
PROMPT_COMMAND='PS1="user> "'
`,
				".shrc":  "PS1='user> '\n",
				".zshrc": "PROMPT='user> '\nprecmd() { PROMPT='user> '; }\n",
			} {
				if err := os.WriteFile(filepath.Join(home, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if shell == "fish" {
				path := filepath.Join(home, ".config", "fish")
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "config.fish"), []byte("function fish_prompt; printf 'user> '; end\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := directoryConfig(home, executable, dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := gtach.Start(ctx, options(cfg))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c, err := gtach.Dial(ctx, cfg.Socket)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := c.Attach(); err != nil {
				t.Fatal(err)
			}
			readPrompt := func(marker string) string {
				t.Helper()
				var out strings.Builder
				b := make([]byte, 1)
				for !strings.HasSuffix(out.String(), "user> ") || !strings.Contains(out.String(), marker) {
					if _, err := io.ReadFull(c, b); err != nil {
						t.Fatalf("prompt: %q: %v", out.String(), err)
					}
					out.Write(b)
				}
				if !strings.HasSuffix(out.String(), "[g] user> ") || strings.Contains(out.String(), "[g] [g]") {
					t.Fatalf("incorrect prompt: %q", out.String())
				}
				return out.String()
			}
			readPrompt("")
			for i := 0; i < 2; i++ {
				if _, err := c.Write([]byte("printf 'INSIDE:%s\\n' \"$GTACH\"\n")); err != nil {
					t.Fatal(err)
				}
				if out := readPrompt("INSIDE:1"); !strings.Contains(out, "INSIDE:1") {
					t.Fatalf("GTACH not set: %q", out)
				}
			}
			if os.Getenv("GTACH") != "outside" {
				t.Fatal("modified caller environment")
			}
		})
	}
}
