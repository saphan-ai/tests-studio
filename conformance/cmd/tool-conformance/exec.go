package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyTree copies the regular files and directories under src into dst. A symlink or any other special file is
// refused: the copy must not point back out of itself.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		default:
			return fmt.Errorf("%s is not a regular file or directory (a symlink?)", rel)
		}
	})
}

// bind returns the values of the typed inputs for one run, all rooted in the fresh copy.
func bind(rec *record, root string) map[string]string {
	v := map[string]string{
		"repo_root":   root,
		"output_path": filepath.Join(root, filepath.FromSlash(rec.Output.Path)),
	}
	if rec.Config != "" {
		v["config_path"] = filepath.Join(root, filepath.FromSlash(rec.Config))
	}
	for name, e := range rec.Params {
		if e.Type == "relpath" {
			v[name] = filepath.Join(root, filepath.FromSlash(e.Value))
		} else {
			v[name] = e.Value
		}
	}
	return v
}

func substitute(el string, vals map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(el, func(m string) string {
		return vals[placeholderRe.FindStringSubmatch(m)[1]]
	})
}

// runResult is what check (c) hands to check (d).
type runResult struct {
	code int
	tail string
}

// runTool executes the record's argv, with no shell, inside root, and waits at most timeout.
func runTool(rec *record, root string, timeout time.Duration) (runResult, error) {
	vals := bind(rec, root)
	argv := make([]string, len(rec.Command))
	for i, el := range rec.Command {
		argv[i] = substitute(el, vals)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	res := runResult{tail: tailOf(buf.String(), 600)}
	if ctx.Err() != nil {
		return res, fmt.Errorf("the tool did not finish within %s and was killed", timeout)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.code = 0
	case errors.As(err, &ee):
		res.code = ee.ExitCode()
		if res.code < 0 {
			return res, fmt.Errorf("the tool was ended by a signal (%v)", ee)
		}
	default:
		return res, fmt.Errorf("the tool did not start: %v", err)
	}
	return res, nil
}

func tailOf(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return s
}

// classOf maps an exit code through exit_map; the key it used comes back for the message.
func classOf(rec *record, code int) (class, key string) {
	key = strconv.Itoa(code)
	if c, ok := rec.ExitMap[key]; ok {
		return c, key
	}
	return rec.ExitMap["default"], "default"
}
