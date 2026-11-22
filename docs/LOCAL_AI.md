# Local AI Setup for MailDiver (100% Private, 0 Cloud Tokens)

MailDiver's AI features (email summaries, reply drafts, daily briefings, event & contact extraction, optional triage) work completely locally on Apple Silicon, Linux, and Windows — no email content ever leaves your machine.

---

## Option 1: MTPLX + Inkling-2-Mini (Recommended on Apple Silicon)

[MTPLX](https://github.com/youssofal/mtplx) runs **Inkling-2-Mini** natively on Apple Silicon via MLX with speculative decoding (MTP), fast tool calling, and long context — without Ollama's slowdowns on long email threads.

### 1. Install MTPLX

```bash
brew install youssofal/mtplx/mtplx
mtplx --version
```

### 2. Start the local model server

```bash
mtplx serve --model Youssofal/Inkling-2-Mini-MTPLX-Optimized-Speed
```

On the first run, MTPLX downloads the model (~17 GB) and starts an OpenAI-compatible server on `http://127.0.0.1:8000/v1` (model ID: `mtplx-inkling-2-mini-optimized-speed`).

*(Tip: stop `ollama serve` if it is running at the same time so both engines don't hold ~20 GB of unified memory simultaneously.)*

### 3. Configure MailDiver

Open **Settings (`⌘,`) → AI & Sync**:

- **Provider:** `llamacpp` *(recommended: 300 s local timeout & "Local · 0 Cloud Tokens" badge; `openaicompat` also works)*
- **Endpoint:** `http://127.0.0.1:8000/v1`
- **Model:** `mtplx-inkling-2-mini-optimized-speed` *(auto-loaded into the dropdown via `/v1/models`)*
- **API Key:** leave empty (no key required on localhost)
- Click **Save**

*(Optional: for strict server-side JSON schema enforcement in MTPLX you can install `llguidance` into MTPLX's venv, though MailDiver v0.7.12+ automatically falls back gracefully without it.)*

### Bonus: Using the same MTPLX server with OpenCode / T3 Code

Not needed for MailDiver itself, but if you also want to wire the same local MTPLX server into OpenCode (or T3 Code):

```bash
mtplx connect opencode
```

---

## Option 2: Ollama (macOS, Linux & Windows)

1. Install [Ollama](https://ollama.com) and pull a tool-capable model:
   ```bash
   ollama pull qwen3:4b
   # or a larger model like qwen2.5:14b
   ```
2. In MailDiver open **Settings (`⌘,`) → AI & Sync**:
   - **Provider:** `ollama`
   - **Endpoint:** `http://127.0.0.1:11434` *(default)*
   - **Model:** pick any installed model from the dropdown
   - Click **Save**

---

## Option 3: llama.cpp / llamafile / Any OpenAI-Compatible Server

Any local server exposing `/v1/chat/completions` and `/v1/models` (llama-server, LM Studio, vLLM, MLX-LM) works out of the box:

- **Provider:** `llamacpp` (local, 300 s timeout) or `openaicompat`
- **Endpoint:** `http://127.0.0.1:<port>/v1`
- **API Key:** leave blank for localhost
