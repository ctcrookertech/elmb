package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config holds runtime configuration resolved from flags and environment.
type Config struct {
	Values map[string]string
}

// ParseValueOverrides parses "x=y&a=b" format into a map.
func ParseValueOverrides(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return nil
	}
	m := make(map[string]string, len(vals))
	for k, v := range vals {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

// Resolve looks up a key: first in Values overrides, then in os.Getenv.
func (c *Config) Resolve(key string) string {
	if c != nil && c.Values != nil {
		if v, ok := c.Values[key]; ok {
			TraceLine("config", "resolved "+key+" from --value override")
			return v
		}
	}
	if v := os.Getenv(key); v != "" {
		TraceLine("config", "resolved "+key+" from environment")
		return v
	}
	return ""
}

// ResolveAPIKey resolves the API key from overrides, env, or config file.
func (c *Config) ResolveAPIKey() string {
	if key := c.Resolve("ELMB_API_KEY"); key != "" {
		return key
	}
	home, err := os.UserHomeDir()
	if err != nil {
		TraceLine("config", "cannot determine home directory: "+err.Error())
		return ""
	}
	keyPath := filepath.Join(home, ".config", "elmb", "anthropic.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		TraceLine("config", "key file not found: "+keyPath)
		return ""
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		TraceLine("config", "rejecting "+keyPath+": permissions too open (want 0600 or stricter)")
		return ""
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		TraceLine("config", "cannot read key file: "+err.Error())
		return ""
	}
	TraceLine("config", "resolved ELMB_API_KEY from "+keyPath)
	return strings.TrimSpace(string(data))
}

// EncodeValues re-serializes the Values map to query-string format.
func (c *Config) EncodeValues() string {
	if c == nil || len(c.Values) == 0 {
		return ""
	}
	vals := url.Values{}
	for k, v := range c.Values {
		vals.Set(k, v)
	}
	return vals.Encode()
}
