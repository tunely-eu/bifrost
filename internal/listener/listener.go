package listener

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Spec struct {
	Type    string `json:"type" yaml:"type"`
	Path    string `json:"path,omitempty" yaml:"path,omitempty"`
	Mode    string `json:"mode,omitempty" yaml:"mode,omitempty"`
	Address string `json:"address,omitempty" yaml:"address,omitempty"`
}

type Options struct {
	AllowedUnixPrefixes []string
	AllowPublicTCP      bool
	CreateParentDirs    bool
}

type Listener struct {
	net.Listener
	Spec Spec
}

func Listen(spec Spec, opts Options) (*Listener, error) {
	if err := Validate(spec, opts); err != nil {
		return nil, err
	}
	switch spec.Type {
	case "unix":
		if opts.CreateParentDirs {
			if err := os.MkdirAll(filepath.Dir(spec.Path), 0o755); err != nil {
				return nil, err
			}
		}
		if err := removeStaleSocket(spec.Path); err != nil {
			return nil, err
		}
		ln, err := net.Listen("unix", spec.Path)
		if err != nil {
			return nil, err
		}
		mode, err := parseMode(spec.Mode)
		if err != nil {
			_ = ln.Close()
			_ = os.Remove(spec.Path)
			return nil, err
		}
		if err := os.Chmod(spec.Path, mode); err != nil {
			_ = ln.Close()
			_ = os.Remove(spec.Path)
			return nil, err
		}
		return &Listener{Listener: ln, Spec: spec}, nil
	case "tcp":
		ln, err := net.Listen("tcp", spec.Address)
		if err != nil {
			return nil, err
		}
		return &Listener{Listener: ln, Spec: spec}, nil
	default:
		return nil, fmt.Errorf("unsupported listener type %q", spec.Type)
	}
}

func (l *Listener) Close() error {
	err := l.Listener.Close()
	if l.Spec.Type == "unix" {
		if removeErr := os.Remove(l.Spec.Path); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
			err = removeErr
		}
	}
	return err
}

func Validate(spec Spec, opts Options) error {
	switch spec.Type {
	case "unix":
		if spec.Path == "" {
			return fmt.Errorf("unix listener requires path")
		}
		if !filepath.IsAbs(spec.Path) {
			return fmt.Errorf("unix listener path must be absolute")
		}
		if !pathAllowed(spec.Path, opts.AllowedUnixPrefixes) {
			return fmt.Errorf("unix listener path %q is outside allowed prefixes", spec.Path)
		}
		if _, err := parseMode(spec.Mode); err != nil {
			return err
		}
	case "tcp":
		if spec.Address == "" {
			return fmt.Errorf("tcp listener requires address")
		}
		if err := validateTCPAddress(spec.Address, opts.AllowPublicTCP); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported listener type %q", spec.Type)
	}
	return nil
}

func String(spec Spec) string {
	switch spec.Type {
	case "unix":
		return "unix:" + spec.Path
	case "tcp":
		return "tcp:" + spec.Address
	default:
		return spec.Type
	}
}

func parseMode(raw string) (os.FileMode, error) {
	if raw == "" {
		raw = "0600"
	}
	value, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid unix listener mode %q", raw)
	}
	if value&^0o777 != 0 {
		return 0, fmt.Errorf("invalid unix listener mode %q", raw)
	}
	return os.FileMode(value), nil
}

func pathAllowed(path string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return false
	}
	cleanPath := filepath.Clean(path)
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		cleanPrefix := filepath.Clean(prefix)
		if cleanPath == cleanPrefix || strings.HasPrefix(cleanPath, cleanPrefix+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func validateTCPAddress(addr string, allowPublic bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid tcp listener address %q: %w", addr, err)
	}
	if allowPublic {
		return nil
	}
	if host == "" {
		return fmt.Errorf("tcp listener %q binds all interfaces; set allow_public_tcp to true to permit this", addr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("resolve tcp listener host %q: %w", host, err)
		}
		for _, resolved := range addrs {
			if !resolved.IsLoopback() {
				return fmt.Errorf("tcp listener host %q is not localhost", host)
			}
		}
		return nil
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("tcp listener host %q is not localhost", host)
	}
	return nil
}

func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", path)
	}
	return os.Remove(path)
}
