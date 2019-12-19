// +build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	go func() {
		for {
			<-c
			downloadBooks()
		}
	}()
}
