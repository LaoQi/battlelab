//go:build !linux && !darwin && !windows

package vbash

var plat = fillPlatform(platformOps{})
