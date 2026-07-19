// Package sourcebind loads the immutable, portable source inputs used by the
// source-first authoring analyzers. It deliberately captures every accepted
// byte once; downstream parsing must consume those captured bytes rather than
// reopening a checkout path.
package sourcebind
