These are logs from the Ollama inference server (`ollama serve`). They mix
three sources:

1. Ollama's own Go server, in logfmt (Go slog) style:

       time=2025-01-15T14:32:01.123+01:00 level=INFO source=sched.go:714 msg="loaded runners" count=1

   `level` is DEBUG, INFO, WARN or ERROR. `source` is the Go file and line.
   The interesting content is in `msg` and the key=value pairs after it.

2. HTTP request logs from the GIN framework, one per finished request:

       [GIN] 2025/01/15 - 14:32:15 | 200 |  12.345678s |  127.0.0.1 | POST  "/api/chat"

   Fields: status code, total request duration, client IP, method, path.
   `/api/chat`, `/api/generate`, `/v1/chat/completions` are inference calls.
   `/api/tags`, `/api/ps`, `/api/version` and HEAD `/` are clients polling;
   they are routine.

3. Output of the llama.cpp runner process that actually holds the model.
   Lines are prefixed with the llama.cpp function that wrote them, e.g.
   `llama_model_loader:`, `llm_load_tensors:` / `load_tensors:`,
   `llm_load_print_meta:` / `print_info:`, `llama_new_context_with_model:` /
   `llama_context:`, `ggml_cuda_init:`. A model load prints dozens of these
   at once; treat that burst as one event.

Key events and how they look:

- Startup: `msg="Listening on 127.0.0.1:11434 (version 0.x.y)"`, then
  `msg="inference compute"` describing each GPU (library=cuda/rocm/metal/cpu,
  name, total and available memory). No GPU found means CPU-only inference.
- Scheduling a model: `msg="new model will fit in available VRAM ..."` or
  `msg=offload ... layers.requested=... layers.model=N layers.offload=M
  memory.required.full=...`. M < N means the model is split between GPU and
  CPU, which is much slower; M = 0 means CPU only.
- Loading: `msg="starting llama server" cmd=...` (shows the model file,
  `--ctx-size`, `--n-gpu-layers`, `--parallel`), then the runner burst
  (architecture, parameter count, quantization type such as Q4_K_M, context
  length, `offloaded M/N layers to GPU`, buffer sizes), then
  `msg="llama runner started in X seconds"`.
- Inference: a GIN line per request with its duration. Timing lines such as
  `prompt eval time = ... ms / N tokens` and `eval time = ... ms / N runs`
  give prefill and generation speed; derive tokens/s when useful.
- Unloading: the model is evicted after its keep-alive expires, or to make
  room for another model. Look for messages about unloading, expiring, or
  `gpu VRAM usage didn't recover within timeout`.

Notable and worth reporting:

- Anything with level=WARN or level=ERROR.
- `msg="truncating input prompt"` — the prompt exceeded the context window
  (`limit=`) and was cut; answers will miss context.
- Out of memory: `out of memory`, `cudaMalloc failed`, `unable to allocate`,
  failed loads, runner exits (`llama runner process has terminated`), and
  `exit status`.
- Partial or zero GPU offload, and models being unloaded and reloaded
  repeatedly (thrashing between models).
- Non-200 status codes in GIN lines, and requests far slower than earlier
  ones.
