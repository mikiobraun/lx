These are logs from the Ollama inference server (`ollama serve`). Details
below are taken from version 0.12.10 at the default log level (INFO); other
versions differ in message wording. They mix three sources:

1. Ollama's own Go server, in logfmt (Go slog) style:

       time=2026-10-03T18:06:49.885+02:00 level=INFO source=sched.go:500 msg="loaded runners" count=1

   `level` is DEBUG, INFO, WARN or ERROR. `source` is the Go file and line.
   The interesting content is in `msg` and the key=value pairs after it.

2. HTTP request logs from the GIN framework, one per finished request:

       [GIN] 2026/10/03 - 18:06:49 | 200 |   2.84918148s |       127.0.0.1 | POST     "/api/generate"

   Fields: status code, total request duration, client IP, method, path.
   A GIN line is written when the request *finishes*, so it comes after the
   lines for the work the request caused (a load, a download), and its
   duration includes that work.

3. Output of the llama.cpp runner process that holds the model. Lines start
   with the llama.cpp function that wrote them, e.g. `llama_model_loader:`,
   `print_info:`, `load_tensors:`, `llama_context:`. A model load prints
   hundreds of these at once; treat that burst as one event.

Endpoints:

- `/api/generate`, `/api/chat`, `/v1/chat/completions`, `/v1/completions`:
  inference. The first request for a model that is not in memory triggers a
  model load. `ollama run` sends an empty `/api/generate` just to load the
  model, so a load is often followed by a fast generate and then chat calls.
- `/api/embed`, `/api/embeddings`, `/v1/embeddings`: embeddings; also load
  the model if needed.
- `/api/show`: model metadata. 404 means the model is not present locally;
  `ollama run` then pulls it.
- `/api/pull`: download a model; comes with the download lines below.
- `HEAD /`, `/api/tags` (list local models), `/api/ps` (list loaded models),
  `/api/version`, `/v1/models`: clients checking the server; routine.

Key events, in the order their lines appear:

- Startup (no endpoint; service start): `source=routes.go msg="server
  config"` (all OLLAMA_* settings, e.g. OLLAMA_CONTEXT_LENGTH,
  OLLAMA_KEEP_ALIVE), `msg="Listening on 127.0.0.1:11434 (version 0.12.10)"`,
  `msg="discovering available GPUs..."` followed by one or more
  `msg="starting runner" cmd="... runner --ollama-engine --port N"` lines.
  These are short-lived probes for GPU discovery, not models being loaded:
  their `cmd` has no `--model`. Then `source=types.go msg="inference
  compute"` per device (`library=cpu` means no GPU; total and available
  memory), and possibly `msg="entering low vram mode"`.
- Model pull (`/api/pull`): `source=download.go msg="downloading <digest>
  in N <size> part(s)"` per layer, then the GIN line for `/api/pull` with
  the total duration.
- Model load (triggered only by an inference or embedding endpoint, never
  by `/api/show`, `/api/pull` or polling):
  1. A first `llama_model_loader:` / `print_info:` burst: the server reads
     the model's metadata before starting a runner.
  2. `source=server.go msg="starting runner" cmd="... runner --model
     <blob path> ..."` (with `--model`, unlike the startup probes),
     `msg="system memory"` (total, free, free_swap),
     `source=memory.go msg="new model will fit in available system memory
     for CPU inference, loading"` (or the VRAM/GPU variant) with
     `required=` memory, and `msg=offload library=cpu layers.model=N
     layers.offload=M memory.required.full=...`. M < N means the model is
     split between GPU and CPU; M = 0 means CPU only.
  3. `source=runner.go msg="starting go runner"`, `msg=load request="{...
     KvSize:4096 NumThreads:4 ...}"` (KvSize is the context size).
  4. A second, larger burst from the runner itself: `llama_model_loader: -
     kv 2: general.name str = Llama 3.2 1B Instruct` (model name),
     `print_info: file type = Q8_0` (quantization), `print_info: model
     params = 1.24 B`, `load_tensors: CPU model buffer size = ...`,
     `llama_kv_cache: size = ...`, `llama_context: ... compute buffer size
     = ...`.
  5. `source=server.go msg="llama runner started in X seconds"` (often
     logged twice for one load) and `source=sched.go msg="loaded runners"
     count=N` (models in memory).
  6. The GIN line of the inference request that caused the load, with a
     duration that includes the load.

  GIN lines that appear just *before* a load, typically `HEAD /` and
  `/api/show` from `ollama run`, belong to requests that had already
  finished. They did not cause the load; the request that did is the one
  whose GIN line comes after it.
- Inference: only the GIN line, with status and duration. Per-request
  timings (prompt eval, tokens/s) are not logged at INFO level.
- Unloading after keep-alive expires is not logged at INFO level. A later
  load of the same model is the only sign it was unloaded.
- Service stop and restart: `Stopping ollama.service` from systemd, then
  the startup lines again.

Notable and worth reporting:

- Anything with level=WARN or level=ERROR.
- `msg="truncating input prompt"`: the prompt exceeded the context window
  (`limit=`) and was cut; answers will miss context.
- Out of memory: `out of memory`, `cudaMalloc failed`, `unable to allocate`,
  failed loads, runner exits (`llama runner process has terminated`), and
  `exit status`.
- Partial or zero GPU offload on a machine that has a GPU, and the same
  model being loaded again and again (keep-alive too short, or models
  evicting each other).
- Non-200 status codes in GIN lines, and requests far slower than earlier
  ones.

Markers for matching lines (each line may be prefixed by a journald header
such as `Oct 03 18:30:46 host ollama[1234]: `, so do not anchor at `^`):

- Server lines by level: `level=(DEBUG|INFO|WARN|ERROR)`.
- Server lines by component. `source=` always carries a line number
  (`source=server.go:522`), so match `source=server\.go:\d+` or just
  `source=server\.go`. Components: `source=routes.go` (startup, config),
  `source=types.go` (hardware), `source=memory.go` and `source=sched.go`
  (scheduling a load), `source=server.go` and `source=runner.go` (runner
  start, memory, offload), `source=download.go` and `source=images.go`
  (pulls and blob storage).
- GIN request lines: `\[GIN\]`. Fields are padded with a varying number of
  spaces, so always use `\s+` around them, never a single space. The status
  code sits between pipes: `\|\s+200\s+\|`, `\|\s+[45]\d\d\s+\|` for errors.
  The path is quoted at the end, after the padded method:
  `POST\s+"/api/generate"`. Matching just the quoted path, e.g.
  `"/api/(generate|chat)"`, is usually enough. List every endpoint of a
  group from the Endpoints section, e.g. inference includes
  `"/v1/chat/completions"`.
- Runner output: the function prefix at the start of the message:
  `llama_model_loader:`, `llama_model_load:`, `print_info:`, `load:`,
  `load_tensors:`, `load_backend:`, `llama_context:`, `llama_kv_cache:`,
  `llama_init_from_model:`. The model name, quantization and size are in
  `general\.name`, `file type`, `model params`.
- Errors outside of `level=`: `out of memory`, `error`, `failed`,
  `terminated`, `exit status`.
