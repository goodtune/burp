// Package useragent holds the User-Agent burp sends on every outbound
// request (GitHub, ghp, ACME), so operators can tell burp's traffic apart
// in proxy and API logs.
package useragent

import "sync/atomic"

const project = "(+https://github.com/goodtune/burp)"

var value atomic.Value

func init() { value.Store("burp/dev " + project) }

// Set records the running version; call it once at startup.
func Set(version string) { value.Store("burp/" + version + " " + project) }

// String returns the User-Agent, e.g. "burp/1.2.3 (+https://github.com/goodtune/burp)".
func String() string { return value.Load().(string) }
