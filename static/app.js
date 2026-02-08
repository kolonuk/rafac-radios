let ws;
let currentLessonID;
let currentStudentID;
let currentName;
let isTutor = false;
let frequencies = [];
let callsigns = [];
let students = [];
let tutors = [];
let lessonType = 'fixed';
let tutorName = '';
let callsignVerify = false;
let pendingCallsigns = [];
let hasPermissions = false;
let recognition;
let currentTranscript = "";

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
            user_id: currentStudentID, // Using currentStudentID as the generic user_id
            name: currentName
        };
        ws.send(JSON.stringify(joinMsg));

        if (!isTutor && hasPermissions) {
            ws.send(JSON.stringify({ type: "update_permissions", has_permissions: true }));
        }
    };

    ws.onmessage = async (event) => {
        if (typeof event.data === "string") {
            const msg = JSON.parse(event.data);
            if (msg.type === "transcript") {
                addTranscript(msg);
            } else if (msg.type === "update") {
                frequencies = msg.frequencies || [];
                callsigns = msg.callsigns || [];
                students = msg.students || [];
                tutors = msg.tutors || [];
                lessonType = msg.lesson_type;
                tutorName = msg.tutor_name;
                callsignVerify = msg.callsign_verify;
                pendingCallsigns = msg.pending_callsigns || [];
                updateUI();
            } else if (msg.type === "force_leave") {
                window.location.href = "/?error=Session ended by tutor";
            } else if (msg.type === "error") {
                window.location.href = "/?error=" + encodeURIComponent(msg.message);
            } else if (msg.type === "role_change") {
                if (msg.new_role === "tutor") {
                    window.location.reload(); // Reload to get tutor UI
                } else if (msg.new_role === "student") {
                    window.location.reload(); // Reload to get student UI
                }
            } else if (msg.type === "tts_broadcast") {
                speak(msg.message);
            } else if (msg.type === "force_permission_request") {
                requestPermissions();
            } else if (msg.type === "download_zip") {
                window.location.href = msg.url;
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
    if (!audioCtx) return;
    try {
        const int16Data = new Int16Array(arrayBuffer);
        const float32Data = new Float32Array(int16Data.length);
        for (let i = 0; i < int16Data.length; i++) {
            float32Data[i] = int16Data[i] / (int16Data[i] < 0 ? 0x8000 : 0x7FFF);
        }

        const audioBuffer = audioCtx.createBuffer(1, float32Data.length, audioCtx.sampleRate);
        audioBuffer.getChannelData(0).set(float32Data);

        const source = audioCtx.createBufferSource();
        source.buffer = audioBuffer;

        const filter = audioCtx.createBiquadFilter();
        filter.type = "bandpass";
        filter.frequency.value = 1500;
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
    } catch (e) {
        console.error("Error playing audio", e);
    }
}

let tutorPTTTarget = null;

function speak(text) {
    if ('speechSynthesis' in window) {
        const utterance = new SpeechSynthesisUtterance(text);
        window.speechSynthesis.speak(utterance);
    }
}

function startTutorPTT(target) {
    const btn = target === 'GLOBAL' ? document.getElementById('global-ptt-btn') : null;
    if (btn) btn.classList.add('active');
    tutorPTTTarget = target;
    ws.send(JSON.stringify({ type: "tutor_ptt", is_ptting: true, target: target, name: tutorName }));
    startRecording();
}

function stopTutorPTT() {
    const btn = document.getElementById('global-ptt-btn');
    if (btn) btn.classList.remove('active');
    ws.send(JSON.stringify({ type: "tutor_ptt", is_ptting: false, name: tutorName }));
    tutorPTTTarget = null;
    stopRecording();
}

function initTutor(lessonID, tutorID, name) {
    isTutor = true;
    currentLessonID = lessonID;
    currentStudentID = tutorID;
    currentName = name;
    tutorName = name;
    initWS();

    const globalPtt = document.getElementById('global-ptt-btn');
    if (globalPtt) {
        globalPtt.onmousedown = () => startTutorPTT('GLOBAL');
        globalPtt.onmouseup = stopTutorPTT;
        globalPtt.ontouchstart = (e) => { e.preventDefault(); startTutorPTT('GLOBAL'); };
        globalPtt.ontouchend = (e) => { e.preventDefault(); stopTutorPTT(); };
    }

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

    document.getElementById('clean-students-btn').onclick = () => {
        if (confirm("Are you sure you want to kick all students?")) {
            ws.send(JSON.stringify({ type: "clean_students" }));
        }
    };

    document.getElementById('clean-freqs-btn').onclick = () => {
        if (confirm("Are you sure you want to delete all frequencies?")) {
            ws.send(JSON.stringify({ type: "clean_frequencies" }));
        }
    };

    document.getElementById('clean-callsigns-btn').onclick = () => {
        if (confirm("Are you sure you want to delete all callsigns?")) {
            ws.send(JSON.stringify({ type: "clean_callsigns" }));
        }
    };

    document.getElementById('clear-freqs-btn').onclick = () => {
        ws.send(JSON.stringify({ type: "clear_frequencies" }));
    };

    document.getElementById('end-ex-btn').onclick = () => {
        ws.send(JSON.stringify({ type: "end_ex" }));
    };

    document.getElementById('end-lesson-btn').onclick = () => {
        if (confirm("End the entire lesson? This will kick all students and close the session.")) {
            ws.send(JSON.stringify({ type: "end_lesson" }));
            // We'll wait for the download_zip message or a timeout before redirecting
            setTimeout(() => {
                window.location.href = "/";
            }, 2000);
        }
    };

    const typeSelect = document.getElementById('lesson-type');
    typeSelect.value = lessonType;
    typeSelect.onchange = () => {
        ws.send(JSON.stringify({
            type: "update_settings",
            lesson_type: typeSelect.value,
            callsign_verify: document.getElementById('callsign-verify').checked
        }));
    };

    const verifyCheck = document.getElementById('callsign-verify');
    verifyCheck.checked = callsignVerify;
    verifyCheck.onchange = () => {
        ws.send(JSON.stringify({
            type: "update_settings",
            lesson_type: typeSelect.value,
            callsign_verify: verifyCheck.checked
        }));
    };

    document.getElementById('request-permissions').onclick = requestPermissions;
    requestPermissions();
    initSpeechRecognition();
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

    document.getElementById('change-freq-btn').onclick = () => {
        if (confirm("Changing your frequency will stop your current transmission. Continue?")) {
            showStudentFreqDialog();
        }
    };

    document.getElementById('change-call-btn').onclick = () => {
        if (confirm("Changing your callsign may require tutor approval. Continue?")) {
            document.getElementById('student-callsign-dialog').showModal();
        }
    };

    document.getElementById('confirm-call-change').onclick = (e) => {
        e.preventDefault();
        const newCall = document.getElementById('new-callsign-input').value;
        if (newCall) {
            ws.send(JSON.stringify({ type: "assign_callsign", callsign: newCall }));
            document.getElementById('new-callsign-input').value = '';
            document.getElementById('student-callsign-dialog').close();
        }
    };

    document.getElementById('create-custom-freq').onclick = (e) => {
        e.preventDefault();
        const newFreq = document.getElementById('custom-freq-name').value;
        if (newFreq) {
            ws.send(JSON.stringify({ type: "add_frequency", frequency: newFreq }));
            ws.send(JSON.stringify({ type: "assign_frequency", frequency: newFreq }));
            document.getElementById('custom-freq-name').value = '';
            document.getElementById('student-freq-dialog').close();
        }
    };

    document.getElementById('request-permissions-student').onclick = requestPermissions;
    requestPermissions();
    initSpeechRecognition();
}

function showStudentFreqDialog() {
    const options = document.getElementById('freq-options');
    options.innerHTML = '';
    frequencies.forEach(f => {
        const btn = document.createElement('button');
        btn.textContent = f;
        btn.className = 'frequency-item';
        btn.onclick = (e) => {
            e.preventDefault();
            ws.send(JSON.stringify({ type: "assign_frequency", frequency: f }));
            document.getElementById('student-freq-dialog').close();
        };
        options.appendChild(btn);
    });

    const openInput = document.getElementById('open-freq-input');
    openInput.style.display = (lessonType === 'open') ? 'block' : 'none';

    document.getElementById('student-freq-dialog').showModal();
}

async function requestPermissions() {
    try {
        microphoneStream = await navigator.mediaDevices.getUserMedia({ audio: true });
        if (!audioCtx) {
            audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        }
        hasPermissions = true;
        const status = isTutor ? document.getElementById('permission-status') : document.getElementById('permission-status-student');
        if (status) status.textContent = "✅ Permissions granted";
        const btn = isTutor ? document.getElementById('request-permissions') : document.getElementById('request-permissions-student');
        if (btn) btn.style.display = 'none';

        if (!isTutor && ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: "update_permissions", has_permissions: true }));
        }
    } catch (err) {
        console.error("Permission denied", err);
        const status = isTutor ? document.getElementById('permission-status') : document.getElementById('permission-status-student');
        if (status) status.textContent = "❌ Permission denied";
    }
}

function initSpeechRecognition() {
    if (!('webkitSpeechRecognition' in window) && !('SpeechRecognition' in window)) {
        console.warn("Speech recognition not supported");
        return;
    }
    const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
    recognition = new SpeechRecognition();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = 'en-GB';

    recognition.onresult = (event) => {
        let interimTranscript = '';
        for (let i = event.resultIndex; i < event.results.length; ++i) {
            if (event.results[i].isFinal) {
                currentTranscript += event.results[i][0].transcript;
            } else {
                interimTranscript += event.results[i][0].transcript;
            }
        }
        // Send interim/final transcript to server
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
                type: "transcript",
                text: currentTranscript + interimTranscript
            }));
        }
    };
}

function startRecording() {
    if (!audioCtx || !microphoneStream) return;

    if (audioCtx.state === 'suspended') {
        audioCtx.resume();
    }

    currentTranscript = "";
    if (recognition) {
        try {
            recognition.start();
        } catch (e) { console.error("Recognition start error", e); }
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

            // Convert to Int16 PCM
            const int16Buffer = new Int16Array(inputData.length);
            for (let i = 0; i < inputData.length; i++) {
                const s = Math.max(-1, Math.min(1, inputData[i]));
                int16Buffer[i] = s < 0 ? s * 0x8000 : s * 0x7FFF;
            }

            if (isTutor && tutorPTTTarget) {
                // Prepend "TUTOR|Target|" to binary data
                const header = `TUTOR|${tutorPTTTarget}|`;
                const headerBytes = new TextEncoder().encode(header);
                const combined = new Uint8Array(headerBytes.length + int16Buffer.buffer.byteLength);
                combined.set(headerBytes);
                combined.set(new Uint8Array(int16Buffer.buffer), headerBytes.length);
                ws.send(combined.buffer);
            } else {
                // Send as Int16 binary data
                ws.send(int16Buffer.buffer);
            }
        }
    };

    sourceNode.connect(filter);
    filter.connect(compressor);
    compressor.connect(processorNode);

    // Use a zero-gain node to keep the processor alive without feedback
    const silentGain = audioCtx.createGain();
    silentGain.gain.value = 0;
    processorNode.connect(silentGain);
    silentGain.connect(audioCtx.destination);
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
    if (recognition) {
        try {
            recognition.stop();
        } catch (e) { console.error("Recognition stop error", e); }
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
    // Update settings UI if they changed from elsewhere
    document.getElementById('lesson-type').value = lessonType;
    document.getElementById('callsign-verify').checked = callsignVerify;

    // Update Tutor list
    const tutorList = document.getElementById('tutor-list');
    tutorList.innerHTML = '';
    tutors.forEach(t => {
        const li = document.createElement('li');
        li.className = 'tutor-item';
        li.innerHTML = `
            <span>${t.name} ${t.is_main ? '(Main)' : ''}</span>
            <div class="tutor-actions">
                ${!t.is_main ? `<button class="mini-btn remove-btn" onclick="demoteTutor('${t.id}')">Demote</button>` : ''}
            </div>
        `;
        tutorList.appendChild(li);
    });

    // Update pending requests
    const pendingRequests = document.getElementById('pending-requests');
    const pendingList = document.getElementById('pending-list');
    pendingList.innerHTML = '';
    if (pendingCallsigns.length > 0) {
        pendingRequests.style.display = 'block';
        pendingCallsigns.forEach(req => {
            const li = document.createElement('li');
            li.className = 'pending-item';
            li.innerHTML = `
                <span>${req.student_name}: <strong>${req.new_callsign}</strong></span>
                <div>
                    <button class="mini-btn" onclick="approveCallsign('${req.student_id}', '${req.new_callsign}')">Approve</button>
                    <button class="mini-btn remove-btn" onclick="denyCallsign('${req.student_id}', '${req.new_callsign}')">Deny</button>
                </div>
            `;
            pendingList.appendChild(li);
        });
    } else {
        pendingRequests.style.display = 'none';
    }

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
            <span class="${!s.has_permissions ? 'missing-permissions' : ''}">${!s.has_permissions ? '⚠️' : ''} ${s.name} ${s.callsign ? `[${s.callsign}]` : ''} ${s.frequency ? `(${s.frequency})` : ''}</span>
            <div class="student-actions">
                ${!s.has_permissions ? `<button class="mini-btn warning" onclick="forcePermissionRequest('${s.id}')" title="Force Permission Request">Req Mic</button>` : ''}
                <button class="mini-btn" onclick="promoteStudent('${s.id}')">Promote</button>
                ${s.frequency ? `<button class="remove-btn" onclick="removeFreq('${s.id}')">X Freq</button>` : ''}
                ${s.callsign ? `<button class="remove-btn" onclick="removeCall('${s.id}')">X Call</button>` : ''}
                <span>${s.is_ptting ? '🎙️' : ''} ${receiving ? '🔊' : ''}</span>
            </div>
        `;
        li.draggable = true;
        li.ondragstart = (e) => {
            e.dataTransfer.setData('studentID', s.id);
        };

        // Tap and hold to listen
        const startListen = (e) => {
            listeningTo = { type: 'student', id: s.id };
            e.currentTarget.classList.add('listening');
        };
        li.onmousedown = startListen;
        li.ontouchstart = (e) => { e.preventDefault(); startListen(e); };

        studentList.appendChild(li);
    });

    const freqList = document.getElementById('frequency-list');
    freqList.innerHTML = '';
    frequencies.forEach(f => {
        const div = document.createElement('div');
        div.className = 'frequency-item';
        if (listeningTo.type === 'frequency' && listeningTo.id === f) div.classList.add('listening');

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
        const startListen = (e) => {
            listeningTo = { type: 'frequency', id: f };
            e.currentTarget.classList.add('listening');
        };
        div.onmousedown = startListen;
        div.ontouchstart = (e) => { e.preventDefault(); startListen(e); };

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

    const tutorDisp = document.getElementById('tutor-name-display');
    if (tutorDisp && tutorName) tutorDisp.textContent = tutorName;

    const freqDisp = document.getElementById('current-frequency');
    freqDisp.textContent = me.frequency || "None (Cleared)";

    const callDisp = document.getElementById('current-callsign');
    if (callDisp) callDisp.textContent = me.callsign || "None";

    // Update buttons visibility based on lesson type
    const changeFreqBtn = document.getElementById('change-freq-btn');
    const changeCallBtn = document.getElementById('change-call-btn');
    if (lessonType === 'fixed') {
        changeFreqBtn.style.display = 'none';
        changeCallBtn.style.display = 'none';
    } else if (lessonType === 'restricted-freq' || lessonType === 'open') {
        changeFreqBtn.style.display = 'inline-block';
        changeCallBtn.style.display = 'inline-block';
    }

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

    // Update freq status list (color changes like tutor)
    const freqStatusList = document.getElementById('freq-status-list');
    if (freqStatusList) {
        freqStatusList.innerHTML = '';
        frequencies.forEach(f => {
            const li = document.createElement('li');
            li.textContent = f;
            li.style.padding = '0.3rem';
            li.style.borderRadius = '4px';
            li.style.marginBottom = '0.2rem';
            li.style.textAlign = 'center';
            li.style.border = '1px solid var(--border-color)';

            const isTransmitting = students.some(s => s.is_ptting && s.frequency === f);
            if (isTransmitting) {
                li.style.backgroundColor = 'var(--error-color)';
                li.style.color = 'white';
            }
            if (me.frequency === f) {
                li.style.fontWeight = 'bold';
                li.style.border = '2px solid var(--primary-color)';
            }
            freqStatusList.appendChild(li);
        });
    }
}

// Global release of listening state for tutors
window.addEventListener('mouseup', () => {
    if (isTutor && listeningTo.type) {
        listeningTo = { type: null, id: null };
        document.querySelectorAll('.listening').forEach(el => el.classList.remove('listening'));
    }
});
window.addEventListener('touchend', () => {
    if (isTutor && listeningTo.type) {
        listeningTo = { type: null, id: null };
        document.querySelectorAll('.listening').forEach(el => el.classList.remove('listening'));
    }
});

function removeFreq(studentID) {
    ws.send(JSON.stringify({ type: "remove_frequency", student_id: studentID }));
}

function removeCall(studentID) {
    ws.send(JSON.stringify({ type: "remove_callsign", student_id: studentID }));
}

function approveCallsign(studentID, callsign) {
    ws.send(JSON.stringify({ type: "approve_callsign", student_id: studentID, callsign: callsign }));
}

function denyCallsign(studentID, callsign) {
    ws.send(JSON.stringify({ type: "deny_callsign", student_id: studentID, callsign: callsign }));
}

function promoteStudent(studentID) {
    ws.send(JSON.stringify({ type: "role_change", target_id: studentID, new_role: "tutor" }));
}

function demoteTutor(tutorID) {
    ws.send(JSON.stringify({ type: "role_change", target_id: tutorID, new_role: "student" }));
}

function forcePermissionRequest(studentID) {
    ws.send(JSON.stringify({ type: "force_permission_request", target_id: studentID }));
}

function addTranscript(msg) {
    const container = document.getElementById('transcript-list');
    if (!container) return;

    let div = document.getElementById(`transcript-${msg.user_id}`);
    if (!div) {
        div = document.createElement('div');
        div.id = `transcript-${msg.user_id}`;
        div.className = 'transcript-item';
        container.prepend(div);
    }

    div.innerHTML = `
        <span class="transcript-meta">[${msg.frequency}] <strong>${msg.callsign}</strong>:</span>
        <span class="transcript-text">${msg.text}</span>
    `;
}

function showTab(tabId) {
    document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));

    document.getElementById(tabId).classList.add('active');
    // Find button that has onclick for this tabId
    if (tabId === 'ptt-tab') document.getElementById('tab-ptt').classList.add('active');
    if (tabId === 'info-tab') document.getElementById('tab-info').classList.add('active');
}
