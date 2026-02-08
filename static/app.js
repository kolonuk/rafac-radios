let ws;
let currentLessonID;
let currentStudentID;
let currentName;
let isTutor = false;
let frequencies = [];
let students = [];

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

    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === "update") {
            frequencies = msg.frequencies || [];
            students = msg.students || [];
            updateUI();
        }
    };

    ws.onclose = () => {
        console.log("Disconnected from WebSocket. Retrying...");
        setTimeout(initWS, 2000);
    };
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
            ws.send(JSON.stringify({
                type: "add_frequency",
                frequency: freqName
            }));
            document.getElementById('new-freq-name').value = '';
            document.getElementById('freq-dialog').close();
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
    };

    const stopPTT = () => {
        if (!pttBtn.classList.contains('active')) return;
        pttBtn.classList.remove('active');
        document.body.classList.remove('transmitting');
        ws.send(JSON.stringify({ type: "ptt", is_ptting: false }));
    };

    pttBtn.onmousedown = startPTT;
    pttBtn.onmouseup = stopPTT;
    pttBtn.ontouchstart = (e) => { e.preventDefault(); startPTT(); };
    pttBtn.ontouchend = (e) => { e.preventDefault(); stopPTT(); };

    window.onkeydown = (e) => {
        if (e.code === 'Space') {
            e.preventDefault();
            startPTT();
        }
    };
    window.onkeyup = (e) => {
        if (e.code === 'Space') {
            e.preventDefault();
            stopPTT();
        }
    };

    document.getElementById('request-permissions-student').onclick = requestPermissions;
    requestPermissions();
}

async function requestPermissions() {
    try {
        await navigator.mediaDevices.getUserMedia({ audio: true });
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

        // Find if anyone else is PTTing on the same frequency
        const receiving = students.some(other => other.id !== s.id && other.is_ptting && other.frequency === s.frequency && s.frequency !== "");
        if (receiving) li.classList.add('receiving-active');

        li.innerHTML = `
            <span>${s.name} ${s.frequency ? `(${s.frequency})` : ''}</span>
            <span>${s.is_ptting ? '🎙️' : ''} ${receiving ? '🔊' : ''}</span>
        `;
        li.draggable = true;
        li.ondragstart = (e) => {
            e.dataTransfer.setData('studentID', s.id);
        };
        studentList.appendChild(li);
    });

    const freqList = document.getElementById('frequency-list');
    freqList.innerHTML = '';
    frequencies.forEach(f => {
        const div = document.createElement('div');
        div.className = 'frequency-item';
        div.textContent = f;
        div.ondragover = (e) => e.preventDefault();
        div.ondrop = (e) => {
            e.preventDefault();
            const studentID = e.dataTransfer.getData('studentID');
            ws.send(JSON.stringify({
                type: "assign_frequency",
                student_id: studentID,
                frequency: f
            }));
        };
        freqList.appendChild(div);
    });
}

function updateStudentUI() {
    const me = students.find(s => s.id === currentStudentID);
    if (!me) return;

    const freqDisp = document.getElementById('current-frequency');
    freqDisp.textContent = me.frequency || "None (Cleared)";

    // Update background color if receiving
    const receiving = students.some(other => other.id !== currentStudentID && other.is_ptting && other.frequency === me.frequency && me.frequency !== "");

    if (receiving) {
        document.body.classList.add('receiving');
    } else {
        document.body.classList.remove('receiving');
    }
}
