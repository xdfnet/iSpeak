//go:build !darwin || !cgo

package main

import "errors"

func newDefaultStreamPlayer() (StreamPlayer, error) {
	return nil, errors.New("AVAudioEngine 播放器仅支持 macOS 且需要启用 cgo")
}
