package main

import (
	"os"
	"os/exec"
)

// GenerateTTS generates audio from text on the server.
// GenerateTTS converts a string of text into raw audio data using a server-side TTS engine (espeak-ng).
func (l *Lesson) GenerateTTS(text string) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "tts-*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// espeak-ng generates a WAV file.
	// We can adjust pitch, speed, etc. to make it sound more like a radio.
	cmd := exec.Command("espeak-ng", "-s", "140", "-p", "50", "-w", tmpFile.Name(), text)
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	audio, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		return nil, err
	}

	// WAV files have a 44-byte header.
	// Since the client expects raw Int16 PCM, we strip the header.
	if len(audio) > 44 {
		return audio[44:], nil
	}
	return audio, nil
}
