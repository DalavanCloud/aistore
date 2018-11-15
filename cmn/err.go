/*
 * Copyright (c) 2018, NVIDIA CORPORATION. All rights reserved.
 */
// Package cmn provides common low-level types and utilities for all dfcpub projects
package cmn

import (
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
)

// as of 1.9 net/http does not appear to provide any better way..
func IsErrConnectionRefused(err error) (yes bool) {
	if uerr, ok := err.(*url.Error); ok {
		if noerr, ok := uerr.Err.(*net.OpError); ok {
			if scerr, ok := noerr.Err.(*os.SyscallError); ok {
				if scerr.Err == syscall.ECONNREFUSED {
					yes = true
				}
			}
		}
	}
	return
}

// Checks if the error is generated by any IO operation and if the error
// is severe enough to run the FSHC for mountpath testing
//
// for mountpath definition, see fs/mountfs.go
func IsIOError(err error) bool {
	if err == nil {
		return false
	}
	if err == io.ErrShortWrite {
		return true
	}

	isIO := func(e error) bool {
		return e == syscall.EIO || // I/O error
			e == syscall.ENOTDIR || // mountpath is missing
			e == syscall.EBUSY || // device or resource is busy
			e == syscall.ENXIO || // No such device
			e == syscall.EBADF || // Bad file number
			e == syscall.ENODEV || // No such device
			e == syscall.EUCLEAN || // (mkdir)structure needs cleaning = broken filesystem
			e == syscall.EROFS || // readonly filesystem
			e == syscall.EDQUOT || // quota exceeded
			e == syscall.ESTALE || // stale file handle
			e == syscall.ENOSPC // no space left
	}

	switch e := err.(type) {
	case *os.PathError:
		return isIO(e.Err)
	case *os.SyscallError:
		return isIO(e.Err)
	default:
		return false
	}
}