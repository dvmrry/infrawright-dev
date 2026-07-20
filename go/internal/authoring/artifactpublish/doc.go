// Package artifactpublish transactionally replaces a caller-owned authoring
// artifact directory with one complete, validated set of detached artifacts.
//
// The package supports cooperative publishers on local Darwin and Linux
// filesystems. The caller must exclusively own the destination and its sibling
// lock, staging, and backup names for the duration of Publish. It deliberately
// does not claim protection from a non-cooperating same-UID writer, NFS lock
// semantics, power loss, or continuous reader availability.
package artifactpublish
