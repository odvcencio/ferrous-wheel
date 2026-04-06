// WASM initialization
const go = new Go();
let wasmReady = false;

const editor = document.getElementById('editor');
const goCode = document.getElementById('go-code');
const stdout = document.getElementById('stdout');
const runBtn = document.getElementById('run');
const shareBtn = document.getElementById('share');
const exampleSelect = document.getElementById('examples');
const status = document.getElementById('status');

// Backend URL — update for production
const BACKEND_URL = ''; // empty = relative path, or set to backend URL

async function initWasm() {
    try {
        const result = await WebAssembly.instantiateStreaming(
            fetch('fw.wasm'), go.importObject
        );
        go.run(result.instance);
        wasmReady = true;
        runBtn.disabled = false;
        transpile(); // Initial transpile if there's content
    } catch (err) {
        goCode.textContent = 'Failed to load WASM: ' + err.message;
    }
}

// Debounced transpilation
let debounceTimer;
function onInput() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(transpile, 300);
}

function transpile() {
    if (!wasmReady) return;
    const source = editor.value;
    if (!source.trim()) {
        goCode.textContent = '';
        return;
    }
    try {
        const resultJson = fwTranspile(source);
        const result = JSON.parse(resultJson);
        if (result.error) {
            goCode.textContent = '// Error: ' + result.error;
        } else {
            goCode.textContent = result.goCode;
        }
        if (result.warnings && result.warnings.length > 0) {
            goCode.textContent += '\n\n// Warnings:\n' + result.warnings.map(w => '// ' + w).join('\n');
        }
    } catch (err) {
        goCode.textContent = '// Transpile error: ' + err.message;
    }
}

// Run button — send Go code to backend
runBtn.addEventListener('click', async () => {
    const code = goCode.textContent;
    if (!code || code.startsWith('// Error')) return;

    status.textContent = 'running...';
    runBtn.disabled = true;
    stdout.textContent = '';

    try {
        const url = BACKEND_URL ? BACKEND_URL + '/api/run' : '/api/run';
        const res = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ code: code }),
        });

        if (res.status === 429) {
            stdout.textContent = 'Rate limited. Please wait a moment.';
            return;
        }

        const data = await res.json();
        if (data.error) {
            stdout.textContent = data.error;
        } else {
            stdout.textContent = data.stdout || '(no output)';
            if (data.stderr) {
                stdout.textContent += '\n\nstderr:\n' + data.stderr;
            }
        }
    } catch (err) {
        stdout.textContent = 'Backend error: ' + err.message + '\n\n(Is the playground backend running?)';
    } finally {
        status.textContent = '';
        runBtn.disabled = false;
    }
});

// Share — encode source in URL hash
shareBtn.addEventListener('click', () => {
    const source = editor.value;
    const encoded = btoa(unescape(encodeURIComponent(source)));
    window.location.hash = 'code=' + encoded;
    navigator.clipboard.writeText(window.location.href).then(() => {
        shareBtn.textContent = 'Copied!';
        setTimeout(() => { shareBtn.textContent = 'Share'; }, 2000);
    });
});

// Load from URL hash
function loadFromHash() {
    const hash = window.location.hash;
    if (hash.startsWith('#code=')) {
        try {
            const encoded = hash.slice(6);
            return decodeURIComponent(escape(atob(encoded)));
        } catch { return null; }
    }
    return null;
}

// Example loading
exampleSelect.addEventListener('change', async (e) => {
    if (!e.target.value) return;
    try {
        const res = await fetch('examples/' + e.target.value + '.fw');
        const source = await res.text();
        editor.value = source;
        transpile();
    } catch (err) {
        editor.value = '// Failed to load example: ' + err.message;
    }
    e.target.value = ''; // reset dropdown
});

// Editor input handler
editor.addEventListener('input', onInput);

// Tab key support in editor
editor.addEventListener('keydown', (e) => {
    if (e.key === 'Tab') {
        e.preventDefault();
        const start = editor.selectionStart;
        const end = editor.selectionEnd;
        editor.value = editor.value.substring(0, start) + '    ' + editor.value.substring(end);
        editor.selectionStart = editor.selectionEnd = start + 4;
        onInput();
    }
});

// Initialize
const hashSource = loadFromHash();
if (hashSource) {
    editor.value = hashSource;
}

// Default content if empty
if (!editor.value) {
    editor.value = `package main

import "fmt"

func main() {
    let name = "world"
    fmt.Println(f"Hello, {name}!")
}
`;
}

initWasm();
