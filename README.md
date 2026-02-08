# Radio Teaching App

A virtual radio training application designed for tutors to conduct simulated radio communication lessons.

## Features

- **Multi-session Support**: Multiple tutors can run separate lessons simultaneously using unique Lesson IDs. The system is optimized to handle high concurrency (e.g., 4+ simultaneous lessons with 12+ students each).
- **Tutor Dashboard**:
    - **Lesson Management**: Choose between Fixed, Restricted Frequency, or Open lesson types.
    - **Real-time Monitoring**: Monitor student statuses and listen to specific frequencies or students using "tap and hold".
    - **Frequency & Callsign Management**: Create and assign frequencies and callsigns via drag-and-drop.
    - **Student Control**: Clean student lists, remove specific assignments, and end the exercise globally.
    - **Callsign Verification**: Option to manually approve or deny student-initiated callsign changes.
- **Student View**:
    - **Push-To-Talk (PTT)**: Half-duplex communication using Mouse/Touch or the Space bar.
    - **Visual Feedback**: Screen turns red when transmitting and green when receiving. Frequency list shows active transmissions.
    - **Callsign & Frequency Choice**: Depending on the lesson type, students can change their own frequency or request a callsign change.
    - **Presence**: See others currently on the same frequency.
- **Realistic Audio**: Integrated Web Audio API filters (bandpass 300Hz-3kHz) and normalization to simulate radio quality.
- **Robust Audio Backends**:
    - **Default (WebSockets)**: High-performance, non-blocking relay optimized for concurrency. Uses Int16 PCM encoding to minimize bandwidth and leverages asynchronous I/O to handle multiple concurrent lessons and transmissions smoothly.
    - **Mumble (Optional)**: Support for professional-grade VoIP using the Mumble protocol (Opus compression, jitter management), ideal for extremely high-traffic or high-latency environments.
- **Admin Portal**:
    - List all running lessons, tutor names, and student counts.
    - Run system tests directly from the browser.

## Getting Started

### Prerequisites

- [Docker](https://www.docker.com/)

### Running the App

#### Option 1: Standard (Docker)
1. Build the Docker image:
   ```bash
   docker build -t rafac-radios .
   ```

2. Run the container:
   ```bash
   docker run -p 8080:8080 -e SYSTEM_CODE=your_secret_code rafac-radios
   ```

#### Option 2: Enhanced Robustness (Docker Compose + Mumble)
This option uses a Mumble server for audio relay, which is more robust for high-traffic or high-latency environments.
1. Run with Docker Compose:
   ```bash
   docker-compose up --build
   ```

3. Access the app:
    - Open `http://localhost:8080` in your browser.
    - **Tutor**: Enter the `SYSTEM_CODE` set in the environment variable, your name, and a unique `Lesson ID`.
    - **Student**: Enter your name and the `Lesson ID` provided by the tutor.
    - **Admin**: Access `http://localhost:8080/admin`.

## Lesson Types

- **Fixed**: Students cannot change their frequency or callsign. Everything is managed by the tutor.
- **Restricted Freq**: Students can choose from frequencies already created by the tutor. They can also change their callsign.
- **Open**: Students can create new frequencies, join any frequency, and change their callsign.

## Technical Details

- **Backend**: Go with Gorilla WebSockets for real-time synchronization.
- **Frontend**: Vanilla JavaScript and CSS (supporting dark/light system themes).
- **Audio**: Web Audio API for capture, processing, and playback.
- **Exclusivity**: Only one transmitter is allowed per frequency at a time (first-come, first-served).
- **Security**: Students must have both a Frequency and a unique Callsign assigned to transmit or receive audio.
- **Isolation**: Each lesson is strictly isolated. Actions in one lesson do not affect others.
