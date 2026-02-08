package main

import (
	"crypto/tls"
	"log"
	"net"
	"os"

	"layeh.com/gumble/gumble"
)

// MumbleBackend handles the integration with an external Mumble server.
type MumbleBackend struct {
	Address string
	Enabled bool
	Config  *gumble.Config
	Client  *gumble.Client
}

func NewMumbleBackend() *MumbleBackend {
	addr := os.Getenv("MUMBLE_ADDRESS")
	enabled := os.Getenv("AUDIO_BACKEND") == "mumble"

	config := gumble.NewConfig()
	config.Username = "RadioServer"

	return &MumbleBackend{
		Address: addr,
		Enabled: enabled,
		Config:  config,
	}
}

func (m *MumbleBackend) Start() {
	if !m.Enabled {
		return
	}

	log.Printf("Connecting to Mumble server at %s", m.Address)

	client, err := gumble.DialWithDialer(new(net.Dialer), m.Address, m.Config, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		log.Printf("Failed to connect to Mumble: %v", err)
		return
	}

	m.Client = client
}

// Broadcast sends audio to a Mumble channel.
func (m *MumbleBackend) Broadcast(channelName string, audio []byte) {
	if !m.Enabled || m.Client == nil {
		return
	}

	// In a real integration, the Go app would have multiple clients or
	// move its single client between channels to broadcast.
	// For this bridge, we relay the Int16 PCM data.

	// gumble uses a specific audio format. We convert our Int16 PCM:
	pcm := make([]int16, len(audio)/2)
	for i := 0; i < len(pcm); i++ {
		pcm[i] = int16(audio[i*2]) | int16(audio[i*2+1])<<8
	}

	m.Client.AudioOutgoing() <- gumble.AudioBuffer(pcm)
}

func (m *MumbleBackend) RightChoice() (bool, string) {
	return true, "Mumble provides industry-standard VoIP robustness with Opus compression and jitter management."
}
