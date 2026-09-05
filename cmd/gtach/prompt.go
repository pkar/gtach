//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Prompt scripts contain no user environment values. They live alongside the
// private sockets, and source normal user startup files before adding a marker.
const bashPrompt = `if [[ -r "$HOME/.bashrc" ]]; then . "$HOME/.bashrc"; fi
__gtach_prompt() {
    local status=$?
    case ${PS1-} in '[g] '*) ;; *) PS1="[g] ${PS1-}" ;; esac
    return "$status"
}
# Preserve scalar hooks on Bash 3.2 and array hooks on newer Bash.
case $(declare -p PROMPT_COMMAND 2>/dev/null) in
    'declare -a '*) PROMPT_COMMAND+=(__gtach_prompt) ;;
    *) PROMPT_COMMAND="${PROMPT_COMMAND-}"$'\n''__gtach_prompt' ;;
esac
`

const shPrompt = `if [ -n "${GTACH_ORIGINAL_ENV-}" ] && [ -r "$GTACH_ORIGINAL_ENV" ]; then
    . "$GTACH_ORIGINAL_ENV"
fi
unset GTACH_ORIGINAL_ENV
case ${PS1-} in '[g] '*) ;; *) PS1="[g] ${PS1-\$ }" ;; esac
`

const zshEnv = `# Preserve the user's startup directory, including changes made by .zshenv.
if [[ $GTACH_ZDOTDIR_SET == 1 ]]; then
    ZDOTDIR=$GTACH_ORIGINAL_ZDOTDIR
else
    unset ZDOTDIR
fi
if [[ -r ${ZDOTDIR:-$HOME}/.zshenv ]]; then source "${ZDOTDIR:-$HOME}/.zshenv"; fi
GTACH_ZDOTDIR_SET=${+ZDOTDIR}
GTACH_ORIGINAL_ZDOTDIR=${ZDOTDIR-}
ZDOTDIR=$GTACH_PROMPT_DIR
`

const zshPrompt = `if [[ $GTACH_ZDOTDIR_SET == 1 ]]; then
    ZDOTDIR=$GTACH_ORIGINAL_ZDOTDIR
else
    unset ZDOTDIR
fi
unset GTACH_ZDOTDIR_SET GTACH_ORIGINAL_ZDOTDIR GTACH_PROMPT_DIR
if [[ -r ${ZDOTDIR:-$HOME}/.zshrc ]]; then source "${ZDOTDIR:-$HOME}/.zshrc"; fi
__gtach_prompt() {
    local last_status=$?
    case $PROMPT in '[g] '*) ;; *) PROMPT="[g] $PROMPT" ;; esac
    return $last_status
}
typeset -ga precmd_functions
precmd_functions+=(__gtach_prompt)
`

const fishPrompt = `functions -c fish_prompt __gtach_original_prompt
function __gtach_restore_status
    return $argv[1]
end
function fish_prompt
    set -l last_status $status
    printf '[g] '
    __gtach_restore_status $last_status
    __gtach_original_prompt
end`

func preparePrompt(c config) (config, error) {
	shell := filepath.Base(c.Command[0])
	root := filepath.Dir(c.Socket)
	write := func(name, script string) (string, error) {
		path := filepath.Join(root, name)
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("refusing to replace prompt path: %s", path)
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
		f, err := os.CreateTemp(root, ".prompt-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(f.Name())
		_, err = f.WriteString(script)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if err := os.Rename(f.Name(), path); err != nil {
			return "", err
		}
		return path, nil
	}
	switch shell {
	case "bash":
		path, err := write("bashrc", bashPrompt)
		if err != nil {
			return c, err
		}
		c.Command = []string{c.Command[0], "--rcfile", path, "-i"}
	case "sh", "dash", "ash", "ksh", "ksh93", "mksh":
		path, err := write("shrc", shPrompt)
		if err != nil {
			return c, err
		}
		c.Env = []string{"GTACH_ORIGINAL_ENV=" + os.Getenv("ENV"), "ENV=" + path}
	case "zsh":
		// Zsh requires filenames .zshenv and .zshrc in its startup directory.
		path := filepath.Join(root, "zsh")
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return c, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return c, err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) {
			return c, fmt.Errorf("unsafe prompt directory: %s", path)
		}
		root = path
		if _, err := write(".zshenv", zshEnv); err != nil {
			return c, err
		}
		if _, err := write(".zshrc", zshPrompt); err != nil {
			return c, err
		}
		old, set := os.LookupEnv("ZDOTDIR")
		flag := "0"
		if set {
			flag = "1"
		}
		c.Env = []string{"GTACH_ZDOTDIR_SET=" + flag, "GTACH_ORIGINAL_ZDOTDIR=" + old, "GTACH_PROMPT_DIR=" + path, "ZDOTDIR=" + path}
	case "fish":
		c.Command = []string{c.Command[0], "-i", "-C", fishPrompt}
	}
	return c, nil
}

func environment(overrides ...string) []string {
	env := os.Environ()
	for _, value := range overrides {
		key, _, _ := strings.Cut(value, "=")
		prefix := key + "="
		filtered := env[:0]
		for _, entry := range env {
			if !strings.HasPrefix(entry, prefix) {
				filtered = append(filtered, entry)
			}
		}
		env = append(filtered, value)
	}
	return env
}
