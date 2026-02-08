let ws;
let currentLessonID;
let currentStudentID;
let currentName;
let isTutor = false;
let frequencies = [];
let callsigns = [];
let students = [];

// Audio variables
let audioCtx;
let microphoneStream;
let processorNode;
let sourceNode;
let listeningTo = { type: null, id: null }; // type: 'student' or 'frequency'

function initWS() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

    ws.onopen = () => {
        console.log("Connected to WebSocket");
        const joinMsg = {
            type: "join",
            lesson_id: currentLessonID,
        };
        if (!isTutor) {
            joinMsg.student_id = currentStudentID;
            joinMsg.name = currentName;
        }
        ws.send(JSON.stringify(joinMsg));
    };

    ws.onmessage = async (event) => {
        if (typeof event.data === "string") {
            const msg = JSON.parse(event.data);
            if (msg.type === "update") {
                frequencies = msg.frequencies || [];
                callsigns = msg.callsigns || [];
                students = msg.students || [];
                updateUI();
            }
        } else {
            // Binary audio data
            handleIncomingAudio(event.data);
        }
    };

    ws.onclose = () => {
        console.log("Disconnected from WebSocket. Retrying...");
        setTimeout(initWS, 2000);
    };
}

async function handleIncomingAudio(data) {
    if (!audioCtx) return;

    let audioData = data;
    let senderID = null;
    let senderFreq = null;

    if (isTutor) {
        // Tutors receive audio with a header: "StudentID|Frequency|"
        const blob = data;
        const arrayBuffer = await blob.arrayBuffer();
        const uint8View = new Uint8Array(arrayBuffer);

        let header = "";
        let offset = 0;
        let pipes = 0;
        while (offset < uint8View.length && pipes < 2) {
            const char = String.fromCharCode(uint8View[offset]);
            header += char;
            if (char === '|') pipes++;
            offset++;
        }

        const parts = header.split('|');
        senderID = parts[0];
        senderFreq = parts[1];
        audioData = arrayBuffer.slice(offset);

        // Filter based on what tutor is listening to
        if (listeningTo.type === 'student' && listeningTo.id !== senderID) return;
        if (listeningTo.type === 'frequency' && listeningTo.id !== senderFreq) return;
        if (!listeningTo.type) return; // Not listening to anything
    } else {
        // Students receive raw audio (already filtered by server to match their freq)
        const blob = data;
        audioData = await blob.arrayBuffer();
    }

    playAudio(audioData);
}

function playAudio(arrayBuffer) {
    // Assuming 16-bit PCM at some sample rate.
    // To keep it simple, we could use AudioContext.decodeAudioData if it's a known format,
    // but for raw PCM we need to create a buffer.
    // Let's assume the sender sends Float32Array for simplicity with Web Audio API.
    const float32Data = new Float32Array(arrayBuffer);
    const audioBuffer = audioCtx.createBuffer(1, float32Data.length, audioCtx.sampleRate);
    audioBuffer.getChannelData(0).set(float32Data);

    const source = audioCtx.createBufferSource();
    source.buffer = audioBuffer;

    // Add filtering and normalization to playback too for "radio quality"
    const filter = audioCtx.createBiquadFilter();
    filter.type = "bandpass";
    filter.frequency.value = 1500; // 1.5kHz center
    filter.Q.value = 1.0;

    const compressor = audioCtx.createDynamicsCompressor();
    compressor.threshold.value = -30;
    compressor.knee.value = 10;
    compressor.ratio.value = 12;
    compressor.attack.value = 0.003;
    compressor.release.value = 0.25;

    source.connect(filter);
    filter.connect(compressor);
    compressor.connect(audioCtx.destination);
    source.start();
}

function initTutor(lessonID, tutorID) {
    isTutor = true;
    currentLessonID = lessonID;
    initWS();

    document.getElementById('create-freq-btn').onclick = () => {
        document.getElementById('freq-dialog').showModal();
    };

    document.getElementById('confirm-freq').onclick = (e) => {
        e.preventDefault();
        const freqName = document.getElementById('new-freq-name').value;
        if (freqName) {
            ws.send(JSON.stringify({ type: "add_frequency", frequency: freqName }));
            document.getElementById('new-freq-name').value = '';
            document.getElementById('freq-dialog').close();
        }
    };

    document.getElementById('create-callsign-btn').onclick = () => {
        document.getElementById('callsign-dialog').showModal();
    };

    document.getElementById('confirm-callsign').onclick = (e) => {
        e.preventDefault();
        const callName = document.getElementById('new-callsign-name').value;
        if (callName) {
            ws.send(JSON.stringify({ type: "add_callsign", callsign: callName }));
            document.getElementById('new-callsign-name').value = '';
            document.getElementById('callsign-dialog').close();
        }
    };

    document.getElementById('clear-freqs-btn').onclick = () => {
        ws.send(JSON.stringify({ type: "clear_frequencies" }));
    };

    document.getElementById('request-permissions').onclick = requestPermissions;
    requestPermissions();
}

function initStudent(lessonID, name) {
    isTutor = false;
    currentLessonID = lessonID;
    currentName = name;
    currentStudentID = 'student_' + Math.random().toString(36).substr(2, 9);
    initWS();

    const pttBtn = document.getElementById('ptt-button');

    const startPTT = () => {
        if (pttBtn.classList.contains('active')) return;
        pttBtn.classList.add('active');
        document.body.classList.add('transmitting');
        ws.send(JSON.stringify({ type: "ptt", is_ptting: true }));
        startRecording();
    };

    const stopPTT = () => {
        if (!pttBtn.classList.contains('active')) return;
        pttBtn.classList.remove('active');
        document.body.classList.remove('transmitting');
        ws.send(JSON.stringify({ type: "ptt", is_ptting: false }));
        stopRecording();
    };

    pttBtn.onmousedown = startPTT;
    pttBtn.onmouseup = stopPTT;
    pttBtn.ontouchstart = (e) => { e.preventDefault(); startPTT(); };
    pttBtn.ontouchend = (e) => { e.preventDefault(); stopPTT(); };

    window.onkeydown = (e) => {
        if (e.code === 'Space' && document.activeElement.tagName !== 'INPUT') {
            e.preventDefault();
            startPTT();
        }
    };
    window.onkeyup = (e) => {
        if (e.code === 'Space' && document.activeElement.tagName !== 'INPUT') {
            e.preventDefault();
            stopPTT();
        }
    };

    document.getElementById('request-permissions-student').onclick = requestPermissions;
    requestPermissions();
}

async function requestPermissions() {
    try {
        microphoneStream = await navigator.mediaDevices.getUserMedia({ audio: true });
        if (!audioCtx) {
            audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        }
        const status = isTutor ? document.getElementById('permission-status') : document.getElementById('permission-status-student');
        if (status) status.textContent = "✅ Permissions granted";
        const btn = isTutor ? document.getElementById('request-permissions') : document.getElementById('request-permissions-student');
        if (btn) btn.style.display = 'none';
    } catch (err) {
        console.error("Permission denied", err);
        const status = isTutor ? document.getElementById('permission-status') : document.getElementById('permission-status-student');
        if (status) status.textContent = "❌ Permission denied";
    }
}

function startRecording() {
    if (!audioCtx || !microphoneStream) return;

    if (audioCtx.state === 'suspended') {
        audioCtx.resume();
    }

    sourceNode = audioCtx.createMediaStreamSource(microphoneStream);

    // Low-pass and High-pass to simulate radio (Bandpass 300Hz - 3kHz)
    const filter = audioCtx.createBiquadFilter();
    filter.type = "bandpass";
    filter.frequency.value = 1500;
    filter.Q.value = 1.0;

    const compressor = audioCtx.createDynamicsCompressor();
    compressor.threshold.value = -30;
    compressor.ratio.value = 12;

    // Use ScriptProcessor for easy binary chunking
    processorNode = audioCtx.createScriptProcessor(4096, 1, 1);

    processorNode.onaudioprocess = (e) => {
        if (ws && ws.readyState === WebSocket.OPEN) {
            const inputData = e.inputBuffer.getChannelData(0);
            // Send as Float32Array binary data
            ws.send(inputData.buffer);
        }
    };

    sourceNode.connect(filter);
    filter.connect(compressor);
    compressor.connect(processorNode);
    processorNode.connect(audioCtx.destination); // Required to keep it running
}

function stopRecording() {
    if (processorNode) {
        processorNode.disconnect();
        processorNode = null;
    }
    if (sourceNode) {
        sourceNode.disconnect();
        sourceNode = null;
    }
}

function updateUI() {
    if (isTutor) {
        updateTutorUI();
    } else {
        updateStudentUI();
    }
}

function updateTutorUI() {
    const studentList = document.getElementById('student-list');
    studentList.innerHTML = '';
    students.forEach(s => {
        const li = document.createElement('li');
        li.className = 'student-item';
        if (s.is_ptting) li.classList.add('ptt-active');
        if (listeningTo.type === 'student' && listeningTo.id === s.id) li.classList.add('listening');

        const receiving = students.some(other => other.id !== s.id && other.is_ptting && other.frequency === s.frequency && s.frequency !== "");
        if (receiving) li.classList.add('receiving-active');

        li.innerHTML = `
            <span>${s.name} ${s.callsign ? `[${s.callsign}]` : ''} ${s.frequency ? `(${s.frequency})` : ''}</span>
            <span>${s.is_ptting ? '🎙️' : ''} ${receiving ? '🔊' : ''}</span>
        `;
        li.draggable = true;
        li.ondragstart = (e) => {
            e.dataTransfer.setData('studentID', s.id);
        };

        // Tap and hold to listen
        const startListen = () => { listeningTo = { type: 'student', id: s.id }; updateTutorUI(); };
        const stopListen = () => { listeningTo = { type: null, id: null }; updateTutorUI(); };
        li.onmousedown = startListen;
        li.onmouseup = stopListen;
        li.ontouchstart = (e) => { e.preventDefault(); startListen(); };
        li.ontouchend = (e) => { e.preventDefault(); stopListen(); };

        studentList.appendChild(li);
    });

    const freqList = document.getElementById('frequency-list');
    freqList.innerHTML = '';
    frequencies.forEach(f => {
        const div = document.createElement('div');
        div.className = 'frequency-item';
        if (listeningTo.type === 'frequency' && listeningTo.id === f) div.classList.add('listening');

        // Check if anyone is transmitting on this frequency
        const isTransmitting = students.some(s => s.is_ptting && s.frequency === f);
        if (isTransmitting) div.classList.add('transmitting-freq');

        div.textContent = f;
        div.ondragover = (e) => e.preventDefault();
        div.ondrop = (e) => {
            e.preventDefault();
            const studentID = e.dataTransfer.getData('studentID');
            if (studentID) {
                ws.send(JSON.stringify({ type: "assign_frequency", student_id: studentID, frequency: f }));
            }
        };

        // Tap and hold to listen
        const startListen = () => { listeningTo = { type: 'frequency', id: f }; updateTutorUI(); };
        const stopListen = () => { listeningTo = { type: null, id: null }; updateTutorUI(); };
        div.onmousedown = startListen;
        div.onmouseup = stopListen;
        div.ontouchstart = (e) => { e.preventDefault(); startListen(); };
        div.ontouchend = (e) => { e.preventDefault(); stopListen(); };

        freqList.appendChild(div);
    });

    const callList = document.getElementById('callsign-list');
    callList.innerHTML = '';
    callsigns.forEach(c => {
        const div = document.createElement('div');
        div.className = 'callsign-item';
        div.textContent = c;
        div.ondragover = (e) => e.preventDefault();
        div.ondrop = (e) => {
            e.preventDefault();
            const studentID = e.dataTransfer.getData('studentID');
            if (studentID) {
                ws.send(JSON.stringify({ type: "assign_callsign", student_id: studentID, callsign: c }));
            }
        };
        callList.appendChild(div);
    });
}

function updateStudentUI() {
    const me = students.find(s => s.id === currentStudentID);
    if (!me) return;

    const freqDisp = document.getElementById('current-frequency');
    freqDisp.textContent = me.frequency || "None (Cleared)";

    const callDisp = document.getElementById('current-callsign');
    if (callDisp) callDisp.textContent = me.callsign || "None";

    // Update background color if receiving
    const isReceiving = students.some(other => other.id !== currentStudentID && other.is_ptting && other.frequency === me.frequency && me.frequency !== "");

    if (isReceiving) {
        document.body.classList.add('receiving');
    } else {
        document.body.classList.remove('receiving');
    }

    // Update others on same frequency list
    const othersList = document.getElementById('others-on-freq');
    if (othersList) {
        othersList.innerHTML = '';
        if (me.frequency) {
            const others = students.filter(s => s.id !== currentStudentID && s.frequency === me.frequency);
            others.forEach(o => {
                const li = document.createElement('li');
                li.textContent = o.callsign ? `${o.callsign} (${o.name})` : o.name;
                if (o.is_ptting) li.style.color = 'var(--error-color)';
                othersList.appendChild(li);
            });
        }
    }
}
