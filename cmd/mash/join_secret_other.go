//go:build !unix

package main

import "os"

// Outside unix there is no owner or mode to read: a plain file is allowed.
const secretOpenFlags = 0

func ownedByMe(os.FileInfo) bool { return true }

func ownerOnly(os.FileInfo) bool { return true }
