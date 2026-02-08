package main

import (
	"os"
	"os/exec"
)

// GenerateTTS generates audio from text on the server.
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

	return os.ReadFile(tmpFile.Name())
}
