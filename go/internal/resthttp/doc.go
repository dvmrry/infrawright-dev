// Package resthttp ports node-src/io/rest-http-transport.ts: the bounded,
// proxy-aware HTTP transport used by the registry-driven collectors.
//
// The package deliberately depends on collectors' narrow HttpTransport seam.
// Collectors must not import this package; command integration owns concrete
// transport construction so the dependency remains one-way.
package resthttp
