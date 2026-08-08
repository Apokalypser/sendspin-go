// ABOUTME: Opus audio encoder for bandwidth-efficient streaming
// ABOUTME: Wraps libopus to encode PCM audio to Opus format
package encode

import (
	"fmt"
	"log"

	"gopkg.in/hraban/opus.v2"
)

// LOCAL PATCH: encoder tunables, see sender/README.md.
//
// Upstream hard-codes the bitrate and leaves the rest at the libopus defaults.
// These are package-level because NewOpusEncoder is only ever reached through
// PlayerRoleConfig.NewEncoder, which Server fills in itself -- there is no
// config path from the application down to here. Set them before NewServer.
//
// Deliberately absent: in-band FEC and DTX. FEC only pays off if the decoder
// asks for it, and the ESP32 receiver calls opus_decode with decode_fec = 0 and
// never signals a lost packet, so the redundancy would cost bitrate and buy
// nothing. DTX drops frames during silence, which a fixed-cadence scheduler
// reads as a gap rather than as quiet.
var (
	// OpusBitratePerChannel is the target bitrate handed to libopus, multiplied
	// by the channel count. Upstream's 128000 puts stereo at 256 kbit/s, which
	// is well past transparency for music -- the reason to lower it is the
	// link, not the ear.
	OpusBitratePerChannel = 128000

	// OpusComplexity is libopus' 0..10 quality-against-CPU dial, spent on the
	// sending host. 9 is the library default; 10 buys very little at these
	// bitrates, and low values are only worth it on a machine that is
	// struggling to encode in real time.
	OpusComplexity = 9
)

// OpusEncoder wraps the Opus encoder
type OpusEncoder struct {
	encoder    *opus.Encoder
	sampleRate int
	channels   int
	frameSize  int // samples per channel per frame
}

// NewOpusEncoder creates a new Opus encoder.
// frameSize is in samples per channel (e.g., 960 for 20ms at 48kHz).
func NewOpusEncoder(sampleRate, channels, frameSize int) (*OpusEncoder, error) {
	// AppAudio mode tunes the encoder for full-bandwidth music rather than speech
	encoder, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	// LOCAL PATCH: bitrate and complexity come from the tunables above.
	bitrate := OpusBitratePerChannel * channels
	if err := encoder.SetBitrate(bitrate); err != nil {
		log.Printf("Warning: Failed to set Opus bitrate: %v", err)
	}
	if err := encoder.SetComplexity(OpusComplexity); err != nil {
		log.Printf("Warning: Failed to set Opus complexity: %v", err)
	}
	log.Printf("Opus encoder: %d Hz, %d ch, %d kbit/s, complexity %d",
		sampleRate, channels, bitrate/1000, OpusComplexity)

	return &OpusEncoder{
		encoder:    encoder,
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
	}, nil
}

// Encode encodes PCM samples to Opus.
// Input: []int16 interleaved samples; output: single Opus packet.
func (e *OpusEncoder) Encode(pcm []int16) ([]byte, error) {
	// Opus spec maximum packet size is 4000 bytes
	output := make([]byte, 4000)

	n, err := e.encoder.Encode(pcm, output)
	if err != nil {
		return nil, fmt.Errorf("opus encode failed: %w", err)
	}

	return output[:n], nil
}

// Close is a no-op; opus.Encoder has no Close method.
func (e *OpusEncoder) Close() error {
	return nil
}
