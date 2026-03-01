//go:build !linux && !darwin

package main

import "syscall"

func procGroupAttr() *syscall.SysProcAttr {
	return nil
}
