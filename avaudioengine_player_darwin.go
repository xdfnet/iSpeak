//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c -fblocks
#cgo LDFLAGS: -framework AVFoundation -framework Foundation

#include <AVFoundation/AVFoundation.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	AVAudioEngine *engine;
	AVAudioPlayerNode *node;
	AVAudioFormat *format;
	pthread_mutex_t mu;
	pthread_cond_t cond;
	int pending;
	int closing;
} AVNativePlayer;

static char *av_make_error(const char *prefix, NSError *error) {
	const char *detail = "";
	if (error != nil && [error localizedDescription] != nil) {
		detail = [[error localizedDescription] UTF8String];
	}
	char buffer[512];
	snprintf(buffer, sizeof(buffer), "%s: %s", prefix, detail);
	return strdup(buffer);
}

static int av_player_create(double sampleRate, unsigned int channels, AVNativePlayer **out, char **err) {
	if (out == NULL) {
		if (err) *err = strdup("av_player_create: out is nil");
		return -1;
	}
	*out = NULL;

	@autoreleasepool {
		AVNativePlayer *player = (AVNativePlayer *)calloc(1, sizeof(AVNativePlayer));
		if (player == NULL) {
			if (err) *err = strdup("av_player_create: calloc failed");
			return -1;
		}

		if (pthread_mutex_init(&player->mu, NULL) != 0) {
			if (err) *err = strdup("av_player_create: mutex init failed");
			free(player);
			return -1;
		}
		if (pthread_cond_init(&player->cond, NULL) != 0) {
			if (err) *err = strdup("av_player_create: cond init failed");
			pthread_mutex_destroy(&player->mu);
			free(player);
			return -1;
		}

		player->engine = [[AVAudioEngine alloc] init];
		player->node = [[AVAudioPlayerNode alloc] init];
		player->format = [[AVAudioFormat alloc] initWithCommonFormat:AVAudioPCMFormatFloat32 sampleRate:sampleRate channels:channels interleaved:NO];
		if (player->engine == nil || player->node == nil || player->format == nil) {
			if (err) *err = strdup("av_player_create: failed to allocate AVAudio objects");
			[player->engine release];
			[player->node release];
			[player->format release];
			pthread_cond_destroy(&player->cond);
			pthread_mutex_destroy(&player->mu);
			free(player);
			return -1;
		}

		[player->engine attachNode:player->node];
		[player->engine connect:player->node to:player->engine.mainMixerNode format:player->format];
		[player->engine prepare];

		NSError *error = nil;
		if (![player->engine startAndReturnError:&error]) {
			if (err) *err = av_make_error("AVAudioEngine start failed", error);
			[player->node release];
			[player->engine release];
			[player->format release];
			pthread_cond_destroy(&player->cond);
			pthread_mutex_destroy(&player->mu);
			free(player);
			return -1;
		}

		[player->node play];
		*out = player;
		return 0;
	}
}

static int av_player_write(AVNativePlayer *player, const void *data, size_t len, char **err) {
	if (player == NULL || player->engine == nil || player->node == nil || player->format == nil) {
		if (err) *err = strdup("av_player_write: player is closed");
		return -1;
	}
	if (data == NULL || len == 0) {
		return 0;
	}

	AVAudioFrameCount frames = (AVAudioFrameCount)(len / 2);
	if (frames == 0) {
		return 0;
	}

	AVAudioPCMBuffer *buffer = [[AVAudioPCMBuffer alloc] initWithPCMFormat:player->format frameCapacity:frames];
	if (buffer == nil) {
		if (err) *err = strdup("av_player_write: failed to allocate buffer");
		return -1;
	}
	buffer.frameLength = frames;
	float *dest = buffer.floatChannelData[0];
	const int16_t *src = (const int16_t *)data;
	for (AVAudioFrameCount i = 0; i < frames; i++) {
		dest[i] = (float)src[i] / 32768.0f;
	}

	pthread_mutex_lock(&player->mu);
	player->pending++;
	pthread_mutex_unlock(&player->mu);

	AVNativePlayer *captured = player;
	[player->node scheduleBuffer:buffer completionHandler:^{
		pthread_mutex_lock(&captured->mu);
		if (captured->pending > 0) {
			captured->pending--;
		}
		if (captured->closing && captured->pending == 0) {
			pthread_cond_signal(&captured->cond);
		}
		pthread_mutex_unlock(&captured->mu);
	}];

	return 0;
}

static int av_player_close_and_dispose(AVNativePlayer *player, char **err) {
	if (player == NULL) {
		return 0;
	}

	pthread_mutex_lock(&player->mu);
	player->closing = 1;
	while (player->pending > 0) {
		pthread_cond_wait(&player->cond, &player->mu);
	}
	pthread_mutex_unlock(&player->mu);

	@autoreleasepool {
		[player->node stop];
		[player->engine stop];
		[player->node release];
		[player->engine release];
		[player->format release];
	}

	pthread_cond_destroy(&player->cond);
	pthread_mutex_destroy(&player->mu);
	free(player);
	return 0;
}

static int av_player_abort(AVNativePlayer *player, char **err) {
	return av_player_close_and_dispose(player, err);
}

*/
import "C"

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"unsafe"
)

const (
	audioEngineSampleRate = 48000
	audioEngineChannels   = 1
	audioEngineChunkSize   = 32 * 1024
)

type avAudioEngineStreamPlayer struct {
	mu   sync.Mutex
	ptr  *C.AVNativePlayer
	tail []byte
}

func newDefaultStreamPlayer() (StreamPlayer, error) {
	var ptr *C.AVNativePlayer
	var cerr *C.char
	if C.av_player_create(C.double(audioEngineSampleRate), C.uint(audioEngineChannels), &ptr, &cerr) != 0 {
		return nil, cError(cerr)
	}
	log.Printf("播放器模式: AVAudioEngine PCM 流式 (%d Hz, %d channel)", audioEngineSampleRate, audioEngineChannels)
	return &avAudioEngineStreamPlayer{ptr: ptr}, nil
}

func (p *avAudioEngineStreamPlayer) Write(audio []byte) error {
	if len(audio) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ptr == nil {
		return errors.New("AVAudioEngine player 已关闭")
	}

	data := audio
	if len(p.tail) > 0 {
		data = append(append([]byte(nil), p.tail...), audio...)
		p.tail = nil
	}

	if len(data)%2 == 1 {
		p.tail = append(p.tail[:0], data[len(data)-1])
		data = data[:len(data)-1]
	}

	for len(data) > 0 {
		n := audioEngineChunkSize
		if n > len(data) {
			n = len(data)
		}
		if n%2 == 1 {
			n--
		}
		if n <= 0 {
			break
		}
		if err := p.writeChunk(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}

	return nil
}

func (p *avAudioEngineStreamPlayer) CloseAndWait() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *avAudioEngineStreamPlayer) writeChunk(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if p.ptr == nil {
		return errors.New("AVAudioEngine player 已关闭")
	}

	var cerr *C.char
	if C.av_player_write(p.ptr, unsafe.Pointer(&data[0]), C.size_t(len(data)), &cerr) != 0 {
		return cError(cerr)
	}
	return nil
}

func (p *avAudioEngineStreamPlayer) closeLocked() error {
	if p.ptr == nil {
		return nil
	}

	if len(p.tail) > 0 {
		pad := append(append([]byte(nil), p.tail...), 0)
		p.tail = nil
		if err := p.writeChunk(pad); err != nil {
			_ = p.disposeLocked()
			return err
		}
	}

	return p.disposeLocked()
}

func (p *avAudioEngineStreamPlayer) disposeLocked() error {
	if p.ptr == nil {
		return nil
	}

	var cerr *C.char
	if C.av_player_close_and_dispose(p.ptr, &cerr) != 0 {
		p.ptr = nil
		return cError(cerr)
	}
	p.ptr = nil
	return nil
}

func cError(cerr *C.char) error {
	if cerr == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(cerr))
	return fmt.Errorf("%s", C.GoString(cerr))
}
