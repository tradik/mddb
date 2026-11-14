package lspindex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The compilation database (#418). clangd learns each file's compiler flags
// — include paths, defines, the language standard — from
// compile_commands.json, which the build writes. Without it clangd guesses,
// and its answers silently miss whatever the guess left out, so autoindex
// looks for one before it starts the server.

// compileCommandsFile is the database's name; clangd reads no other.
const compileCommandsFile = "compile_commands.json"

// ErrNoCompileCommands is returned by FindCompileCommands when there is no
// compilation database where it looked.
var ErrNoCompileCommands = errors.New("no compilation database (" + compileCommandsFile + ")")

// FindCompileCommands returns the directory holding compile_commands.json
// for the checkout at root (absolute). given, when set, is that directory or
// the file itself, relative to the working directory; otherwise root and
// root/build are tried, where CMake and Meson builds usually put it.
func FindCompileCommands(root, given string) (string, error) {
	if given != "" {
		// Abs fails only without a working directory; "" is then no database.
		dir, _ := filepath.Abs(given)
		if filepath.Base(dir) == compileCommandsFile {
			dir = filepath.Dir(dir)
		}
		if isFile(filepath.Join(dir, compileCommandsFile)) {
			return dir, nil
		}
		return "", fmt.Errorf("%w in %s", ErrNoCompileCommands, given)
	}
	build := filepath.Join(root, "build")
	for _, dir := range []string{root, build} {
		if isFile(filepath.Join(dir, compileCommandsFile)) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%w in %s or %s", ErrNoCompileCommands, root, build)
}

// isFile reports whether path names a regular file (or a link to one).
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
