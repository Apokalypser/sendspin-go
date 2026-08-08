// ABOUTME: Audio streaming orchestration for Server
// ABOUTME: Tick-driven chunk generation, codec negotiation, per-client encode/send
package sendspin

import (
	"encoding/binary"
	"log"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
)

func (s *Server) streamAudio() {
	log.Printf("Audio streaming started")

	ticker := time.NewTicker(time.Duration(ChunkDurationMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.generateAndSendChunk()
		case <-s.stopChan:
			log.Printf("Audio streaming stopping")
			return
		}
	}
}

func (s *Server) generateAndSendChunk() {
	// Timestamp invariants — do not weaken without re-analysis:
	//   1. playbackTime is sampled fresh from the monotonic clock on every
	//      tick. It is NOT a running counter like `pending += chunkDurationUs`.
	//   2. ChunkDurationMs × sampleRate must divide evenly by 1000 at every
	//      supported rate. At 20ms this holds: 44.1k→882, 48k→960, 88.2k→1764,
	//      96k→1920. At 25ms, 44.1k→1102.5 (fractional) — do NOT change the
	//      constant without also re-working the chunk-size/sample math.
	// Weakening either invariant re-introduces the drift class described in
	// aiosendspin#217 (500ms cliff after ~17 minutes at 44.1k/25ms). See also
	// issue #91 for converting the linear resampler to integer-rational math.
	// LOCAL PATCH -- see sender/README.md
	//
	// Upstream samples the clock fresh on every tick. That avoids the drift class
	// the invariant note above describes, but it also copies the ticker's delivery
	// jitter straight into the timestamp: the audio content always advances by
	// exactly one chunk, while the timestamp advances by however late the tick was.
	//
	// Measured on a normally loaded macOS host: mean interval exactly 20.00 ms, so
	// no drift, but a standard deviation around 500-1400 us and single excursions
	// to 30-41 ms. A receiver whose own playback clock is stable to +-30 us sees
	// each excursion as a step in the server timeline and, above its hard-sync
	// threshold of 5 ms, corrects with an audible jump.
	//
	// So advance a counter by exactly one chunk instead, and re-anchor it to the
	// sampled clock only when the two have drifted apart by more than
	// playbackReanchorUs. Jitter well below that bound is filtered out completely;
	// real drift is still bounded, which is what the invariant is actually about.
	// currentTime stays the real clock: the send-buffer tracker below prunes
	// against actual elapsed time, which must not be scheduled.
	currentTime := s.getClockMicros()
	sampled := currentTime + (BufferAheadMs * 1000)
	chunkUs := int64(ChunkDurationMs) * 1000

	if s.nextPlaybackTimeUs == 0 {
		s.nextPlaybackTimeUs = sampled
	} else if delta := sampled - s.nextPlaybackTimeUs; delta > playbackReanchorUs ||
		delta < -playbackReanchorUs {
		log.Printf("playback schedule re-anchored, was %.1f ms off", float64(delta)/1000.0)
		s.nextPlaybackTimeUs = sampled
	}

	playbackTime := s.nextPlaybackTimeUs
	s.nextPlaybackTimeUs += chunkUs

	chunkSamples := (s.audioSource.SampleRate() * ChunkDurationMs) / 1000
	totalSamples := chunkSamples * s.audioSource.Channels()

	samples := make([]int32, totalSamples)
	n, err := s.audioSource.Read(samples)
	if err != nil {
		s.consecutiveReadErrs++
		// Log every error for the first few, then throttle
		if s.consecutiveReadErrs <= 3 || s.consecutiveReadErrs%50 == 0 {
			log.Printf("Error reading audio source (%d consecutive): %v", s.consecutiveReadErrs, err)
		}
		// After 1 second of failures (50 ticks at 20ms), notify clients
		if s.consecutiveReadErrs == 50 {
			log.Printf("Audio source failed for 1s, sending stream/end to all clients")
			s.notifyStreamEnd()
		}
		return
	}
	s.consecutiveReadErrs = 0

	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for _, c := range s.clients {
		var audioData []byte
		var encodeErr error

		c.mu.RLock()
		codec := c.codec
		opusEncoder := c.opusEncoder
		flacEncoder := c.flacEncoder
		resampler := c.resampler
		tracker := c.bufferTracker
		c.mu.RUnlock()

		switch codec {
		case "opus":
			if opusEncoder != nil {
				samplesToEncode := samples[:n]

				// Resample when source rate != 48kHz (Opus is locked to 48kHz)
				if resampler != nil {
					outputSamples := resampler.OutputSamplesNeeded(len(samplesToEncode))
					resampled := make([]int32, outputSamples)
					samplesWritten := resampler.Resample(samplesToEncode, resampled)
					samplesToEncode = resampled[:samplesWritten]
				}

				samples16 := convertToInt16(samplesToEncode)
				audioData, encodeErr = opusEncoder.Encode(samples16)
				if encodeErr != nil {
					log.Printf("Opus encode error for %s: %v", c.name, encodeErr)
					continue
				}
			} else {
				continue
			}
		case "flac":
			if flacEncoder != nil {
				audioData, encodeErr = flacEncoder.Encode(samples[:n])
				if encodeErr != nil {
					log.Printf("FLAC encode error for %s: %v", c.name, encodeErr)
					continue
				}
			} else {
				continue
			}
		case "pcm":
			audioData = encodePCM(samples[:n])
		default:
			audioData = encodePCM(samples[:n])
		}

		chunk := CreateAudioChunk(playbackTime, audioData)

		if tracker != nil {
			chunkDurationUs := int64(ChunkDurationMs) * 1000
			tracker.PruneConsumed(currentTime)
			if !tracker.CanSend(len(chunk), chunkDurationUs) {
				if s.config.Debug {
					log.Printf("Buffer full for %s, skipping chunk (%d bytes buffered)",
						c.name, tracker.BufferedBytes())
				}
				continue
			}
		}

		if err := c.SendBinary(chunk); err != nil {
			if s.config.Debug {
				log.Printf("Error sending audio to %s: %v", c.name, err)
			}
			continue
		}

		// LOCAL PATCH: throughput accounting, see Server.AudioBytesSent. Counted
		// here rather than in SendBinary so it stays audio only.
		s.audioBytesSent.Add(int64(len(chunk)))

		if tracker != nil {
			chunkDurationUs := int64(ChunkDurationMs) * 1000
			chunkEndTimeUs := playbackTime + chunkDurationUs
			tracker.Register(chunkEndTimeUs, len(chunk), chunkDurationUs)
		}
	}
}

func (s *Server) notifyStreamEnd() {
	streamEnd := protocol.StreamEnd{
		Roles: []string{"player"},
	}

	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for _, c := range s.clients {
		if c.HasRole("player") {
			if err := c.Send("stream/end", streamEnd); err != nil {
				log.Printf("Error sending stream/end to %s: %v", c.name, err)
			}
		}
	}
}

// negotiateCodec picks the best codec by scanning the client's advertised
// formats in order. The client controls preference (via --preferred-codec);
// the server accepts the first codec it can handle.
//
// Supported: pcm (at source rate), flac, opus. Falls back to pcm.
func negotiateCodec(c *ServerClient, sourceSampleRate int) string {
	if c.capabilities == nil {
		return "pcm"
	}

	for _, format := range c.capabilities.SupportedFormats {
		switch format.Codec {
		case "pcm":
			if format.SampleRate == sourceSampleRate && format.BitDepth == DefaultBitDepth {
				return "pcm"
			}
		case "flac":
			return "flac"
		case "opus":
			return "opus"
		}
	}

	return "pcm"
}

func strPtr(s string) *string {
	return &s
}

// CreateAudioChunk packs timestamp + payload into a Sendspin binary frame:
// [1 byte message type][8 byte big-endian timestamp (µs)][audio bytes].
func CreateAudioChunk(timestamp int64, audioData []byte) []byte {
	chunk := make([]byte, 1+8+len(audioData))
	chunk[0] = AudioChunkMessageType
	binary.BigEndian.PutUint64(chunk[1:9], uint64(timestamp))
	copy(chunk[9:], audioData)
	return chunk
}

// CreateArtworkChunk packs an artwork frame: [1 byte message type][8 byte timestamp (us)][image bytes].
// Channel is 0-3, mapping to the artwork channel message types.
func CreateArtworkChunk(channel int, timestamp int64, imageData []byte) []byte {
	chunk := make([]byte, protocol.BinaryMessageHeaderSize+len(imageData))
	chunk[0] = byte(protocol.ArtworkChannel0MessageType + channel)
	binary.BigEndian.PutUint64(chunk[1:protocol.BinaryMessageHeaderSize], uint64(timestamp))
	copy(chunk[protocol.BinaryMessageHeaderSize:], imageData)
	return chunk
}

// convertToInt16 converts int32 samples to int16 (for Opus encoding)
func convertToInt16(samples []int32) []int16 {
	result := make([]int16, len(samples))
	for i, s := range samples {
		result[i] = int16(s >> 8)
	}
	return result
}

// encodePCM encodes int32 samples as 24-bit PCM bytes
func encodePCM(samples []int32) []byte {
	output := make([]byte, len(samples)*3)
	for i, sample := range samples {
		output[i*3] = byte(sample)
		output[i*3+1] = byte(sample >> 8)
		output[i*3+2] = byte(sample >> 16)
	}
	return output
}
