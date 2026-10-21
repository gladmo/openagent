# openagent: Go 1:1 rewrite of pi/packages/agent

Reference: `../pi` at commit `ff72faba2` (packages: agent v0.87.1 + ai/chord/telemetry closures). Pinned exception: the MCP stack is ported from `a276dabe5` (see the M phases and the [Agent Note](.agents/notes/implemented/architecture/2026-10-14-mcp-port-ahead-of-baseline.md)).Approved plan: mirror modules/names/behavior; dependency closures as separate Go packages; tests ported core-first (e2e + benchmarks excluded).

Extension port (user-approved plan + [Agent Note](.agents/notes/proposed/architecture/2026-09-30-coding-agent-port.md)): pi-tui v0.87.1 (~18.8K LOC) and pi-coding-agent v0.87.1 (~75.3K LOC) 1:1 to Go as `tui/` + `codingagent/` + `cmd/openagent`; same mapping conventions; forced deviations (jiti TS extension eval, NAPI clipboard, photon WASM, highlight.js/marked/grok-mermaid subsets, bun runtime) documented in PARITY.md.

## Status

| Phase | Scope | Status |
|---|---|---|
| P0 | base libs: abort, jsonx, typebox, partialjson, diff, ignore | **done** |
| P1 | telemetry (full) + tests | **done** |
| P2 | chord closure (root json/types, context, delta/index) + tests | **done** |
| P3 | ai closure (types, models, faux, utils subset, auth subset) + tests | **done** |
| P4 | agent root pkg (types, stream-fn, agent-loop, agent, proxy, search) + tests | **done** |
| P5 | harness top-level + utils + tools + env/nodejs (Go: env/local) + tests | **done** |
| P6 | session core + jsonl + session/testing + tests | **done** |
| P7 | runtime (lane, drive pipeline, reducer, restore) + agent-harness + tests | **done** |
| P8 | chord tracker/services subset + pico3 + prioritized tests | **done** |
| P9 | PARITY.md export audit + full suite + JSONL interop spot check | **done** |
| CA0 | groundwork: scope docs, Agent Note, phase table | **done** |
| CA1 | tui/ full port (colors, utils, keys, components, diff renderer) + tests | **done** (native NAPI modules = documented deviation; index.ts barrel inlined) |
| CA2 | codingagent foundation: config, utils core, event-bus/exec basics, semver/minimatch/lockfile | pending |
| CA3 | codingagent/core/tools (bash, edit+diff, read, write, grep, find, ls, powershell, renderers) | **done** (execution logic complete; renderers/ + render-utils defer to CA8 interactive where Theme lives — renderers are display-only) |
| CA4 | extensions (Go-native API + runner + loader w/ jiti deviation), resources, settings, trust | pending |
| CA5 | model stack (registry/resolver/runtime/composer) + session-manager + compaction + cache | **done** (LLM-call halves of compaction/branch summarization + cache-warmer defer to CA6 runtime where streaming lands; remote-catalog/radius slots documented gaps) |
| CA6 | agent-session + runtime/services + sdk facade + system-prompt | pending |
| CA7 | headless modes (print, json-event, rpc/) + cli/ + migrations + main + package-manager + export-html + cmd entries | **mostly done** (print/json/rpc + cli args + cmd/openagent live and binary-verified; package-manager/export-html/migrations remain) |
| CA8 | interactive mode (interactive-mode, theme, ~40 components) + clipboard/image/highlight deviations | **in progress** (theme core + syntax highlight + export-html + extension runner done; interactive-mode UI wiring remains) |
| CA9 | experimental/ (coordinator, mini, micro, services, radius, server) | pending |
| CA10 | PARITY.md tui+codingagent audit, module docs, full gates | pending |
| M0 | mcp/ package: jsonrpc + protocol + transports (stdio/streamable-http/in-memory) + client + oauth closure + tests (from pi/packages/mcp @ a276dabe5) | **done** |
| M1 | codingagent MCP runtime: mcp-servers/config/log/oauth/tools-helpers/runtime + `openagent mcp` CLI + supporting agent/ai/truncate additions + tests | **done** |
| M2 | MCP extension host wiring: extensions/mcp/index.ts + ui.ts + ToolDefinition wrappers (exposure/namespace/outputSchema), /mcp command, codemode/tool_search activation | pending (needs CA4/CA6 surfaces) |

Commands: `gofmt -l .` (empty), `go vet ./...`, `go test ./...` — all green as of P0.

## Package layout

```
abort/        AbortSignal/AbortController/Any (platform shim; nil-safe)
jsonx/        JS-compatible JSON model: insertion-ordered Obj, Parse,
              Stringify (JS number formatting), Clone (structuredClone), Equal
typebox/      npm typebox 1.3.27 subset: builders (Object/String/Number/
              Integer/Boolean/Null/Array/Optional/Union/Literal/Enum/Any/
              Record), Serialize (TypeBox key order), Convert (Value.Convert
              incl. NaN semantics), Compile/Check/Errors (engine keyword
              order: type -> required -> additionalProperties -> properties
              -> items/arrays -> string/number constraints -> const/enum ->
              allOf/anyOf/oneOf; en_US messages)
partialjson/  npm partial-json 0.1.7 port (Allow bitmask, golden-tested)
diff/         npm jsdiff 8.0.4 port: DiffLines (Myers w/ diagonal pruning,
              component coalescing), StructuredPatch/CreateTwoFilesPatch/
              FormatPatch (FILE_HEADERS_ONLY); byte-verified against jsdiff
              run under node
ignore/       npm ignore 7.0.8 semantic port: parsed-pattern wildmatch
              (**/classes/POSIX/escapes), last-match-wins negation table,
              parent-walk inheritance, case-insensitive by default,
              trailing-globstar min-one rule; golden-tested vs npm ignore
telemetry/    full port: contract (StartSpan[T] generic helper for the TS
              generic method), schema structs, DefineTelemetrySchema,
              CreateTypedSpanStarter (runtime only), NOOP_TELEMETRY_CONTEXT
              (single inert span = context), InMemoryTelemetryContext
              (settlement order, detached snapshots, post-settle inertness,
              explicit-status precedence); SpanStatus.Equal for value
              comparison (TS deepStrictEqual); telemetry/testing conformance
              suite (Proxy/unreadable + undefined-rejection sub-cases dropped
              -- no Go equivalent)
mcp/          pi/packages/mcp @ a276dabe5, 1:1: JSON-RPC over jsonx,
              protocol type views, stdio (process groups) + Streamable
              HTTP (SSE, reconnect, auth retry) + in-memory transports,
              client (timeout/abort/progress), OAuth closure (RFC 8414
              discovery, PKCE + DCR + CIMD flow, provider state, loopback
              callback server)
chord/        root: JsonValue=any (nil|bool|float64|string|[]any|*jsonx.Obj),
              CopyJson (cycle detection via ptr identity; arrays by backing
              ptr+len), IsJsonValue, StrictJSONError
chord/context/ Context chain 1:1 (ContextKey[T] identity = *keyToken,
              marker interface exposes keyToken; WithAbortSignal combines via
              abort.Any; WithoutAbortSignal stores typed-nil mask; WithCancel;
              AwaitWithContext fails waiter only; BackgroundContext/
              TodoContext; String() chain rendering)
chord/delta/   Op algebra: 7 typed op structs (Replace/Set/Delete/Append/
              Truncate/Splice/Permute), OpFromJSON/OpsFromJSON = full
              assertValidOp validation, OpToJSON positional tuples,
              Apply/applyOne via nodeChain writeback closures (objects mutate
              in place; array headers propagate up), ApplyImmutable(Batches)
              with owned-set sharing (objects only; ShallowClone keeps
              untouched subtrees shared), Encoder/Decoder wire codec (intern
              on 2nd use, adjacent-path omission, base resets dictionary),
              Overlap (byte-based), ReservedSegments/UnsafePathError/PathError.
              NOTE: Go trap found twice — never shadow range vars in type
              switches (`else if i, ok := seg.(int)`); jsonx.Stringify needs
              reflect fallback for typed slices like [][]any
ai/           P3 IN PROGRESS. DONE so far:
              types.go (content blocks, Usage w/ pointer CacheWrite1h/
              Reasoning, StopReason consts, DeferredHandle, diagnostics,
              System/User/Assistant/ToolResult messages, Content=string|blocks,
              Tool/ToolReference, Context vs TranscriptContext (brand ->
              constructor), 12 AssistantMessageEvent variants w/ shared
              *AssistantMessage Partial, Model catalog subset);
              event_stream.go (EventStream[T,R]: cond-var queue no
              backpressure, Push drops after terminal, Result() single-shot
              never-rejecting, End(result...), AssistantMessageEventStream);
              utils.go (ContentText/GetSystemMessageText/
              RenderSystemMessageUpdate; UUIDv7: 48-bit ts + 41-bit monotonic
              big.Int sequence seeded from random bytes[1..5], mutex-guarded,
              explicit-timestamp mode preserved; diagnostics);
              codec.go (MessageToJSON/MessageFromJSON with EXACT TS field
              order verified by tests; SystemMessage.SectionOrder for
              insertion-ordered sections; ToolToJSON/ToolFromJSON);
              ai_test.go (uuid format/monotonic/concurrency, event stream
              push-pull/end/result, content text, message codec field-order
              round trips).
              P3 DONE additionally (round 2): ai/json_parse.go (RepairJSON,
              ParseJSONWithRepair, ParseStreamingJSON/ParseStreamingJSONObject
              incl. control-char repair + hex literals in coercion),
              ai/retry.go (RetryPolicy/RetryDelayMs/DefaultMaxAgentRetryDelayMs
              60000/IsRetryableAssistantError non-retryable-first +
              RetryAssistantCall w/ callbacks + abort-in-backoff normalization
              to stopReason aborted, sleep via timeAfterFunc indirection for
              tests), ai/estimate.go (CalculateContextTokens,
              EstimateTextTokens rune-based, EstimateMessageTokens,
              EstimateContextTokens w/ last-usage-prefix rule),
              ai/transcript.go (CreateInitialSystemMessage, NormalizeContext,
              Get/Current* replay, Collapse/ResolveTranscript, ToToolDeclaration
              (JSON round-trip), DeclarationsEqual, GetToolStateChanges,
              GetDeclaredTools, HasToolRedefinitions/NonAdditive,
              ResolveTranscriptTools), ai/validation.go (ValidateToolCall/
              ValidateToolArguments w/ ALWAYS-ON raw coercion pass, format
              "Validation failed for tool X:...Received arguments:\n<pretty>",
              prettyJSON indent-2, normalizeOptionalNulls w/ $ref-string rule
              + nil-validator no-drop, coerceWithJSONSchema full port),
              ai/frame.go (11 frame types + FrameToJSON/FrameFromJSON +
              AssistantMessageFrameEncoder w/ covered-chars dedup +
              toolcall checkpoint catchup/isJsonPrefix legacy grammar +
              ReduceAssistantMessageFrames replay; FRAME TESTS PORTED from
              assistant-message-frame.test.ts: authoritative ends, thinking
              redaction, unfinished tool JSON, queued-dedup (NOTE: encode
              AFTER events accumulated), checkpoint, legacy resume, compact
              empty-start, pre-gen error, invalid sequences, interleaved,
              purity), ai/models_store.go (InMemoryModelsStore + ModelsError +
              withCauseDetail + chat assert).
              P3 ROUND-3 additions: ai/options.go (StreamOptions/
              SimpleStreamOptions/Deferred{Fetch,Cancel}Options, ProviderEnv/
              Headers/Response, FetchFunction, auth subset: Credential/
              ApiKeyAuth/ProviderAuth/AuthContext + NewAPIKeyAuth/
              AlwaysConfiguredAuth); ai/models.go (Provider + Models/
              MutableModels interfaces + CreateModels registry w/ RWMutex;
              Stream/StreamSimple normalize Context then dispatch;
              FetchDeferred/CancelDeferred via deferredCapableProvider
              upgrade; errorStream encodes failures in-stream; CalculateCost
              tier selection + 2x cacheWrite1h; GetSupportedThinkingLevels/
              ClampThinkingLevel (xhigh/max need explicit map entries, null
              map marks unsupported); ModelsAreEqual; HasApi);
              ai/faux_provider.go (full createFauxCore port: FIFO response
              queue popped synchronously, goroutine fill (queueMicrotask),
              streamWithDeltas full event protocol w/ {...partial} shallow
              copies per event, chunked 3-5 token random splits, abort checks
              between chunks -> aborted error events, prompt-cache simulation
              keyed by sessionId common prefix (cacheRetention none
              disables), deferred scripting (pendingFetches countdown,
              final resolution with deferred/signal/onResponse stripped,
              cancel marks cancelled + records structuredClone), fauxProvider
              -> ai.Provider adapter w/ AlwaysConfiguredAuth); FIXTURE BUGS
              FOUND: FauxModel must take api/provider identity from the core
              (FauxModelWithIdentity); streamWithDeltas "no stop reason" must
              convert to an in-stream error event (pushDeltasOrError) else
              Result() deadlocks.
              TEST PORTS DONE: retry.test.ts (all 18 behaviors), validation.
              test.ts (coercion table, optional-null omission, $ref string
              rule, nullable unions incl oneOf/anyOf, error message format),
              overflow.test.ts (all provider messages + Xiaomi + exclusions),
              text.test.ts, uuid.test.ts subset (format/order/follower/
              boundaries/rejects), context-estimate behaviors, faux-provider.
              test.ts core subset (identity rewrite, block helpers, queue,
              factories/errors, prompt cache, event order, tool calls,
              explicit error/aborted).
              NOT PORTED (documented deviations): api/lazy.ts lazyStream
              (internal dispatch machinery; Go Models delegates directly),
              auth context/credential-store/resolve impls (agent never calls
              login/logout/refresh; GetAuth returns provider auth directly),
              models.ts refresh/checkAuth/login/logout/generateImages/
              classify, images/classifier model types, compat option unions,
              event-stream.test.ts remainder (covered inline).
              P4 ROUND-3 DONE: agent/types.go (StreamFn, ToolExecution/
              QueueMode consts, Before/AfterToolCall{Context,Result} w/
              pointer optionality, AgentTurnContext/Decision, loop updates,
              CustomAgentMessage extension point (role+timestamp+fields
              jsonx obj), AgentToolResult, AgentTool (label/
              prepareArguments/execute/replay/executionMode), AgentContext,
              AgentEvent 11 variants, AgentLoopConfig = ai.SimpleStreamOptions
              + model/reasoning + all hooks as nilable funcs);
              agent/stream_fn.go (SetDefaultStreamFn/GetDefaultStreamFn);
              agent/agent_loop.go (AgentLoop/AgentLoopContinue event-stream
              wrappers via ai.EventStream w/ agent_end terminal +
              messages extractor; RunAgentLoop/Continue w/ declareToolChanges
              + emit agent_start/turn_start/message_start/end per injected
              prompt; runLoop EXACT sequencing: prepareNextTurn snapshot ->
              steering re-poll only when prior poll empty -> turn_start ->
              inject prepared+pending w/ tool-change declaration ->
              prepareRequest (context/model/thinking swap) -> streamAssistant
              (transformContext -> convertToLlm -> normalizeContext -> API
              key resolution -> shared-partial accumulator + message_update
              w/ raw provider event -> authoritative final replaces tail) ->
              error/aborted hard exit -> length-stop fails all tool calls ->
              executeToolCalls sequential (or any executionMode=sequential)
              / parallel (prep inline, lazy thunks in goroutines w/
              WaitGroup, tool_execution_end in completion order, tool-result
              message_start/end in source order) -> finishTurn end/continue
              -> steering poll -> outer follow-up/explicit-continuation;
              prepareToolCall (prepareArguments -> validateToolArguments ->
              beforeToolCall block+terminate -> abort checks); execute w/
              onUpdate tool_execution_update; finalize w/ afterToolCall
              field overlay; createErrorToolResult/createToolResultMessage
              normalize nil content/details; GO TRAPS FIXED: nil-guard
              optional hooks (TS ?.()), slice-append must propagate out of
              runLoop (return it), parallel test needs valid tool args else
              validation-failure immediates skip Execute; TS copies the
              caller's context array (tests assert on returned newMessages);
              agent/agent.go (Agent: mutex-guarded state w/ SystemPrompt()
              replay, Tools/Messages get+set w/ top-level copy, IsStreaming/
              StreamingMessage/PendingToolCalls/ErrorMessage, subscribe
              ordered sequential invocation w/ run signal + panic outside
              run, PendingMessageQueue all|one-at-a-time peek/drain,
              Steer/FollowUp/clear/has/peek (steering first), Signal/Abort/
              WaitForIdle, Reset retains replayed baseline system message,
              PromptText/Prompt/ContinueFrom (assistant tail drains
              steering then follow-ups w/ skipInitialSteeringPoll), create
              LoopConfig wiring (onPayload/onResponse/onProviderStreamEvent/
              transport/thinkingBudgets/maxRetryDelayMs/sessionId into
              SimpleStreamOptions; PrepareNextTurnWithContext || PrepareNext
              Turn w/ run signal), runWithLifecycle + handleRunFailure
              synthetic aborted|error message via processEvents reducer;
              state reduction: message_start/update -> streamingMessage,
              message_end appends, tool start/end pending set copy, turn_end
              errorMessage, agent_end clears streaming);
              agent/search.go (SearchQuery/SessionSearchHit/EntrySearchHit/
              SessionSearchService).
              P4 TEST PORTS: agent_loop_test.go (event sequence w/ exact
              order, tool calls+results, length-truncation failure, parallel
              completion-order vs source-order persistence (channel-gated),
              terminate batch stops loop, custom messages via convertToLlm,
              continue validation + from tool result);
              agent_test.go (default state, custom initial state w/ initial
              system message declaring tools, lifecycle events, async
              subscriber awaited before prompt resolves, run failure
              lifecycle + errorMessage state, prompt-while-streaming
              rejected, steering queue drain, abort, reset baseline,
              setter copy semantics, subscriber signal, continue drains
              follow-ups then rejects assistant tail).
              P4 ROUND-4 DONE: agent/proxy.go (StreamProxy: POST
              {proxyUrl}/api/stream w/ Bearer auth; whitelisted options
              serialization via jsonx; SSE data: line scan; client-side
              partial rebuild incl. toolcall delta parseStreamingJson
              (hidden \x00partialJson key on args obj stands in for the TS
              partialJson field, cleared on end); signatures restored from
              end events; done/error set stopReason/usage/
              providerThinkingLevel; clean EOF w/o terminal -> synthesized
              "Connection closed by proxy server..." error; HTTP errors ->
              "Proxy error: <status>|<body.error>"; abort via Go ctx) +
              proxy_test.go (5 cases incl. the 3 TS ones: toolcall_end-only
              metadata, non-newline-terminated terminal, clean-EOF error,
              plus auth header + text/thinking streaming w/ signatures).
              agent_loop_test2.go: beforeToolCall mutation without
              revalidation, prepareToolCallArguments, sequential forced by
              tool executionMode (overlap counter), finishTurn after tool
              results before turn_end, action:end skips queue polling
              (providerCalls==1, steering polls only initial), blocked call
              terminate (Execute never runs, reason surfaces, no re-request),
              afterToolCall overlay (content/isError/terminate).
              P4 REMAINING (optional, fold into later rounds if time):
              remaining test cases: continuation single-request natural vs
              context-only, prepareRequest replacement + no steering poll,
              prepareNextTurn snapshot + late steering pickup, tool-update
              gating after settle (agent.test), steering-vs-follow-up
              priority from non-assistant tail, queues kept on finishTurn
              end, session id/options forwarding.
              P5 ROUND-4 STARTED: agent/harness/types.go (Result[TValue,
              TError] generic + Ok/Err/GetOrThrow(pans)/GetOrUndefined/
              ToError; FileError/FileErrorCode, ExecutionError,
              CompactionError, BranchSummaryError; Skill/PromptTemplate/
              AgentHarnessResources; AgentHarnessTool embedding
              agent.AgentTool w/ harness-native Execute signature (onUpdate
              full-snapshot + checkpoint flag, toolContext, invocation w/
              memos, chord Context); AgentHarnessStreamOptions + Patch (nil
              header values delete, nil map clears -- Has*/Clear* flags);
              FileKind/FileInfo/TextLine/TextLineReader + FileSystem
              interface (20 methods, Result-returning, Cwd()); ShellOutput
              Retention/Limits/CaptureOptions/Metadata/View + 4-variant
              Update union + ShellExecResult/Options + Shell interface;
              ExecutionEnv = FS + Shell).
              agent/harness/utils_truncate.go (TruncationResult 13 fields,
              TruncateHead: first-line-over-limit -> empty +
              FirstLineExceedsLimit, complete lines only, lines-vs-bytes
              attribution; TruncateTail: backwards accumulation, partial
              last line via truncateStringToBytesFromEnd rune-boundary;
              FormatSize B/KB/MB 1-decimal; TruncateLine 500-rune +
              "... [truncated]"; UTF8ByteLength = len (Go strings are
              UTF-8); NOTE Go slices make truncateStringToBytesFromEnd
              simpler than the TS surrogate dance).
              agent/harness/utils_usage.go (EmptyUsage/AddUsage w/ optional
              cacheWrite1h+reasoning only-when-present; Context = chord
              Context re-exports incl. BackgroundContext/TodoContext/
              WithAbortSignal/WithoutAbortSignal/WithCancel (generic
              functions can't be re-exported as vars -- use qualified);
              telemetryContextKey + Get/WithTelemetryContext (nil -> NOOP);
              DefaultRetryPolicy (enabled, 3 retries, 1000ms), Validate
              ToolNames/RetryPolicy/CompactionSettings).
              agent/harness/utils_output_capture.go (OutputCapture: push/
              finish/setSpillPath/snapshot/dispose, tail|head retention,
              total lines = newlines + partial, buffer guard at 4x maxBytes
              w/ trimToLast/FirstUtf8Bytes; sanitize strips C0 except
              \t\n (\r IS stripped -- matches TS regex [\x00-\x08\x0b-
              \x1f]) + U+FFF9-FFFB; updateFrom -> replace|metadata|append|
              slide w/ suffixPrefixOverlap probe 64-then-1 bounded 8
              candidates; ApplyShellOutputUpdate folds updates into views;
              NO rate limiting yet -- AdaptivePublisher deferred, flush is
              immediate).
              harness tests: Result, truncate head/tail limits + first-line
              + partial-last + formatSize + truncateLine, sanitize
              semantics, output-capture append-delta + tail retention +
              applyUpdate.
              P5 ROUND-5 DONE: utils_adaptive_publisher.go (generic
              AdaptivePublisher[TValue,TUpdate]: markDirty/flush(force)/
              dispose, first-dirty-immediate, nextEmitAt = now +
              max(minIntervalMs, bytes*1000/targetBps), commit-before-
              publish, single trailing timer under mutex, injectable nowFn);
              utils_shell_output.go (ExecuteShellWithCapture compat
              collector: tail capture defaults + spill, incremental onChunk
              only for append/slide/first-replace (no duplicate bytes on
              metadata/post-cap), aborted || ctx-signal -> cancelled result,
              returnExecutionErrors -> executionError field, Sanitize
              BinaryOutput alias); messages.go (4 custom roles as structs
              embedding agent.CustomAgentMessage -- BashExecutionMessage/
              CustomMessage/BranchSummaryMessage/CompactionSummaryMessage
              w/ Role() overrides; summary prefixes/suffixes;
              BashExecutionToText (fenced output, cancelled/exit-code/
              truncated-spill footers); factories accepting string|number
              timestamps; harness ConvertToLlm mapping custom roles to user
              messages, excludeFromContext filter); skills.go + system-
              prompt.go (LoadSkills: recursive walk, first SKILL.md per dir,
              root .md requires description frontmatter, .gitignore/.ignore/
              .fdignore via openagent/ignore w/ per-dir pattern prefixing
              (prefixIgnorePattern: ! negation, backslash escapes, leading
              / strip, dir gets trailing /), name validation (== parent,
              ^[a-z0-9-]+$, <=64, no edge/double hyphen), description
              <=1024, YAML frontmatter via gopkg.in/yaml.v3, symlink ->
              canonicalPath resolve, LoadSourcedSkills generics;
              FormatSkillInvocation w/ references-relative-to dir;
              FormatSkillsForSystemPrompt <available_skills> XML block w/ 5-
              char XML escaping, disableModelInvocation filter);
              prompt_templates.go (LoadPromptTemplates dir non-recursive/
              explicit file, description fallback = first non-empty body
              line at 60 chars + "...", LoadSourced generics, ParseCommand
              Args shell quotes (quote chars open quotes ANYWHERE -- it's ->
              "its quoted" single arg!), SubstituteArgs $N/${@:N}/${@:N:L}/
              $ARGUMENTS/$@ with $@ last, FormatPromptTemplateInvocation).
              NOTE: go.mod now has the single planned external dep
              gopkg.in/yaml.v3 v3.0.1.
              P5 tests: skills (SKILL.md load, name-mismatch diagnostic,
              gitignore exclusion, root-md frontmatter requirement),
              system-prompt XML + escaping + hidden filter, skill invocation
              format, bashExecutionToText exact output, ConvertToLlm custom
              roles + exclusion, prompt templates (dir load + name order +
              description fallback truncation, explicit file, missing skip,
              parseCommandArgs incl. quote-anywhere semantics, substituteArgs
              all placeholders, invocation format).
              P5 ROUND-6 DONE: telemetry.go (full AI_TELEMETRY_SCHEMA
              pi.ai.request w/ 6 start + 15 end attributes incl. stop_reason
              values + stream timing, HARNESS_TELEMETRY_SCHEMA 11 spans:
              run/compaction/navigation (root_or_external, operation start
              attrs + outcome values), checkpoint/turn (parents run),
              step (4 parents, attempt+compaction.reason required), tool
              (turn|run parents, 7 required start attrs + is_error end),
              hook (any, outcome blocked|completed|failed), sleep (5
              parents, attempt+delay), event_handler (any, handler_error
              end), pi.session.write (session id + write count); exported
              AITelemetrySchema/HarnessTelemetrySchema/AgentTelemetrySchemas
              + StartAiSpan/StartHarnessSpan[T] binding telemetry parent
              from context and deriving span-context via WithTelemetry
              Context); events.go (HarnessEventBus: On w/ ID-based
              registration + idempotent unsubscribe (func values are not
              comparable in Go -- registry pattern), EmitBatch binds
              recipients per event (later registrations never join a
              batch), structuredClone per recipient, handler_error re-emit
              never re-enters (reportErrors=false second pass), lane
              passthrough, Close freezes + On panics; bufferedEventWatcher:
              buffering->started->unsubscribed, resnapshot dropping/holding
              phases w/ epoch bump, held events replay after snapshot swap,
              start-once, enqueue skips stale epochs); hooks.go (Hook
              Registry: 11 hook names, ordered registrations w/ id, Run ->
              aggregate dispatch; before_run accumulates injected messages
              (prompt grows for later handlers), before_tool last-args-wins
              + first-block-breaks (throw -> block w/ message),
              transform_context folds both fields, before_request applies
              stream-options patches + returns cumulative CreateStream
              OptionsPatch diff, before_payload/after_response chain single
              value, after_tool field-wise overlay (content/details/
              isError/usage/terminate; later handlers see earlier overlays),
              firstStructural first decline-or-result (decline+field error
              -> skip), before_drive fails closed (rethrow, stops chain);
              tool hooks in pi.harness.hook spans w/ outcome blocked|
              completed|failed + error status; ApplyStreamOptionsPatch/
              CreateStreamOptionsPatch full nil-delete/nil-clear semantics
              + JSON codecs for options/patches).
              P5 tests: events (clone isolation, unsubscribe, handler_error
              isolation + lane, batch contiguity, close), watcher lifecycle
              + resnapshot drop/hold/replay + start-once, telemetry schemas
              (span presence, required flags, serializable,
              StartAiSpan context propagation), hooks (before_run prompt
              accumulation + error report, before_tool block-breaks/args-
              last/throw-blocks, after_tool overlay chains, transform fold,
              firstStructural decline/decline+field error, before_drive
              fail-closed, stream-options patch nil-delete/nil-clear/diff-
              empty, hook spans recorded w/ outcome).
              P5 ROUND-7 DONE: execution_gate.go (AbortRequested w/
              Cancellation <-chan struct{}; CreateGate -> Gate/ GateControl
              views: open/aborting/closed states, admit panics Abort
              Requested|close error, beginAbort idempotent-guarded,
              signalAbort aborts controller w/ AbortRequested reason,
              close aborts w/ error); execution_tools.go (PreparedToolCall/
              ImmediateToolOutcome/BeforeToolDecision/ClearedToolCall/
              ExecutedToolCall/AfterToolPatch/FinalizedToolCall; Prepare
              ToolCall resolve->prepareArguments->validate (unknown tool/
              validation -> immediate; NOTE numbers coerce via always-on
              coercion pass -- invalid means MISSING REQUIRED like TS call
              ({})), ApplyBeforeToolDecision block-w/-terminate or
              revalidate replacement args, ExecuteToolCall gate-admitted w/
              abort check + acceptingUpdates gating + throw->error result,
              FinalizeToolCall field-wise patch overlay + terminate
              extraction, ToolResultFromMessage/CreateToolResultMessage);
              execution_assistant.go (AssistantResponseMetadata,
              AssistantStreamObserver, HarnessAssistantStreamConfig;
              createRequestOptions maps AgentHarnessStreamOptions -> ai.
              SimpleStreamOptions incl. reasoning only when != off, signal
              from ctx, telemetry context, onPayload wiring, onResponse
              metadata capture; ConsumeAssistantStream state machine: one
              start only, updates-after-start, done-after-start, observer.
              end always runs, afterResponse AbortRequested waits
              cancellation + keeps settled, other errors propagate;
              StreamHarnessAssistant: transformContext -> toProvider
              Messages -> ai.Context{systemPrompt,tools} -> request w/
              captured metadata -> consume).
              execution tests: gate lifecycle (admit/AbortRequested/closed/
              signal), prepare (unknown/missing-required/coerced-number/
              shim), before-decision (passthrough/block+terminate/
              revalidate/missing-required-reject), execute+finalize (patch
              overlay, throw->error), message round-trip, consume lifecycle
              counts, double-start rejection, AbortRequested cancellation
              wait + other-error propagation, wiring (systemPrompt/tools/
              reasoning!=off/timeout/onPayload hook).
              P5 ROUND-8 DONE: agent/harness/tools/ package — tools.go
              (path-utils: NormalizeToolPath unicode-space -> ASCII + @
              strip; ResolveToolPath; ResolveReadToolPath 5 variants
              (exact, narrow-NBSP before AM/PM., NFD approx, curly
              apostrophe, NFD+curly); image.go: DetectSupportedImageMime
              Type (JPEG rejects 0xf7 JPEG-LS, PNG validates IHDR length
              13 + rejects APNG acTL-before-IDAT, GIF87a/89a, RIFF/WEBP,
              BMP DIB sanity planes=1 + bpp in {1,4,8,16,24,32} + header
              offsets), EncodeBase64); builtin.go (ExecutionToolContext
              {env}; WithFileMutationQueue: per-env state keyed by
              canonical path (not_found/not_supported -> absolute), chain
              via closed channel, delete-if-head on release; CreateWrite
              Tool (mutation queue + abort pre/post, parent dirs);
              CreateReadTool (image path w/ optional processor + BMP
              omitted message; text: offset 1-indexed, limit slice, offset
              beyond end error, truncateHead w/ first-line-exceeds sed
              hint, lines/bytes footers w/ next offset, user-limit "more
              lines" footer -- NOTE TS allLines includes trailing empty
              entry after final \n, so remaining counts include it)));
              edit.go (PrepareEditArguments legacy shapes: JSON-string
              edits, single-edit object, top-level oldText/newText merged
              + stripped; validate >=1 edit; CreateEditTool: mutation
              queue, fileInfo kind file|symlink check, stripBom ->
              detectLineEnding -> normalizeToLF -> applyEdits ->
              restoreLineEndings+BOM -> write, abort checks between
              phases, details {diff, patch, firstChangedLine});
              edit_diff.go (DetectLineEnding first-CRLF-vs-LF, Normalize/
              RestoreLineEndings, NormalizeForFuzzyMatch (trailing ws,
              smart quotes/dashes/special spaces -> ASCII; NFKC approx by
              explicit mappings), FuzzyFindText exact-first, fuzzy offsets
              in normalized space; ApplyEditsToNormalizedContent: empty
              oldText / not-found / duplicate (fuzzy count) / overlap /
              no-change error taxonomy w/ singular-vs-indexed messages;
              fuzzy path via ApplyReplacementsPreservingUnchangedLines
              (line-span groups widened, per-group reverse application,
              original bytes preserved elsewhere); GenerateUnifiedPatch =
              diff pkg w/ FILE_HEADERS_ONLY; GenerateDiffString numbered
              +/-/' ' lines w/ padStart width, context elision ... markers
              (leading/trailing/both), firstChangedLine = new-file line).
              FIXTURE BUGS FIXED: literal U+FEFF in Go source is illegal
              (use \ufeff escape -- an earlier global replace had
              gutted StripBom into stripping 1 byte always); 5 test
              expectations corrected to TS semantics (exact match beats
              fuzzy; duplicate beats no-change; trailing context line in
              numbered diff; remaining-count includes trailing empty line;
              8-vs-7).
              tools tests: image sniffing table, base64, NormalizeTool
              Path, read-path variants (narrow-NBSP AM/PM), line-ending
              detection, fuzzy normalization + exact-first, exact edits,
              error taxonomy (duplicate/empty/missing/no-change/overlap),
              fuzzy preserve-lines, unified patch + numbered diff, write
              tool (parents + success text), read tool (full/limit footer/
              beyond-end/image passthrough), edit tool end-to-end (file
              bytes, diff, patch, firstChangedLine=3, legacy prepare).
              P5 ROUND-9 DONE (P5 COMPLETE): tools/bash.go (CreateBash
              Tool: timeout validation (<=0 invalid, >2^31/1000s max),
              BashExecution mutable via Prepare + commandPrefix, initial
              empty onUpdate snapshot, env.Exec w/ tail capture defaults +
              spill, onUpdate fold -> view via ApplyShellOutputUpdate ->
              full snapshot w/ 2s-changed-encoded checkpoint throttle,
              truncation footers (partial last line w/ size, lines, bytes
              limit), timeout/aborted/other status messages w/ output
              prefixed, exit != 0 error w/ code, "(no output)" fallback;
              nil onUpdate guarded); agent/harness/env/local/ [renamed from
              env/nodejs -- see Agent Note env-local-rename] (real
              ExecutionEnv: resolvePath ~ + ~/ + file:// + abs/cwd;
              toFileError errno map not_found/permission/not_directory/
              is_directory/invalid -> codes; full FileSystem impl w/ abort
              short-circuits; OpenTextLineReader pull reader w/ strict LF
              + Terminated flag; shell: getShellConfig (/bin/bash ->
              which bash -> sh; custom shellPath must exist), Setpgid
              process group + killProcessTree (-pgid SIGKILL fallback pid),
              combined stdout+stderr pipe, timeout timer, abort listener,
              exit code from ExitError, OutputCapture-backed views via
              onUpdate; CleanupShell kills tracked pids; spill-file write
              deferred -- capture wiring only, documented).
              Tests: env FS (path resolution, write/read/exists/remove
              recursive+force, not-found code, kinds, listdir, rename+
              canonical, readTextLines limit, line reader termination),
              exec (echo via onUpdate view, exit 3, timeout 0.3s, invalid
              timeout zero, aborted via ctx signal, custom shell missing),
              bash tool e2e (output, exit 7 error, timeout, invalid
              timeout, no-output fallback, prefix+prepare mutation).
              P5 COMPLETE.
              P6 ROUND-10 STARTED: agent/harness/session/ — values.go
              (Value/ValueList w/ validateAddress (empty ns / NUL),
              SetValue/DeleteValue/AppendList/DeleteList write constructors
              + WriteFromValue/WriteFromList union adapters,
              ResolveListReadOptions default 1000 cap 10000, all reserved
              pi.* accessors (BranchTip/LaneConfig/LaneStateValue/
              OperationResult/OperationMetaValue/OperationStateValue/
              OperationToolArgs(+Prefix)/OperationToolMemo(+Prefix)/
              OperationPreparation(+Prefix)/PendingEntryValue/
              PendingToolOutput(+Prefix)/PendingAssistantFrames/
              SessionName/EntryLabel) -- names suffixed Value where the
              TS type name collides with the Go accessor; NUL separator in
              physicalKey needs \u0000 escape, literal NUL is ILLEGAL in Go
              source (same trap as BOM); MutationLine: single worker
              goroutine FIFO, Run returns outcome channel, Seal rejects
              later/queued jobs); types.go (EntryBase + Entry union w/
              message/compaction/branch_summary/custom fields,
              AgentMessagePayload {role, jsonx obj}; OperationMeta w/ jsonx
              Intent; Control running|cancel_requested; flat
              OperationState 13 At-leaves w/ scope/checkpoint/generation/
              tools/deferred/summary/navigation payloads as jsonx +
              OperationScopeOf; LaneState/InboxItem/PendingEntry/
              DurableFileOperations/UsageRow; Write union; CommitResult;
              BranchScan/StorageBranchScan/EntryScan/UsageScan;
              SessionStats; Storage/SessionReader/SessionMutation/
              SessionMutator/Session/Branch/ForkOptions/SessionRepo
              interfaces); commit.go (CommittedWrite w/ ID() helper,
              InsertEntry/InsertUsage, CommitWrite assigns per-write seq +
              shared entry timestamp, PrepareStorageCommit,
              ValidateCommittedWrites: monotonic seqs, entry+usage share id
              namespace, parent in prior state or same transaction);
              fork_policy.go (SelectBranchFork tip->root walk w/ before/at
              position, ProjectForkCurrentStateWrite per-namespace
              (session.name always, entry.label iff copied, branch.tip
              tree=all/branch=rewritten-tip, lane.config scope match,
              lane.state fresh idle, pi.result/op.*/pending.* dropped,
              unknown reserved pi.* panics, app values tree-only)); memory_
              state.go (InMemoryStorageState: entries map + bySeq +
              scalars + lists + usage + stats + nextSeq from 1; Prepare
              Commit validates at high-water; ApplyValidated updates
              messageCount + addUsage stats; CreateFork w/ plan.view()
              adapter + lane config/state existence check; code-point
              compareKeys; ScanValues prefix sort; ReadList cursor
              asc(seq>) / desc(seq<) + reverse + limit; ScanBranch
              walk + stop + filter + cursor + limit; ScanEntries bySeq
              both directions; ScanUsage seq window + order;
              AdvanceNextSeq high-water).
              session tests: address validation, reserved accessors, list
              read options, mutation line FIFO + seal, commit prepare/
              validate/apply (parent chains, same-transaction parent,
              duplicate id, shared entry-usage id namespace), value
              set/replace/delete/recreate, list pagination desc cursor +
              atomic delete/re-append, branch scan + stop-at-type +
              scanEntries desc limit.
              P6 ROUND-11 DONE: session/session.go (5 typed session errors
              (Invariant/InvalidBranch/BranchExists/PendingAssistant/
              UnknownTarget); storageBackedSessionMutation: active flag +
              exactly-one-commit guard + pending-assistant rejection +
              release on End; StorageBackedBranch: GetTipID, FindEntries
              newestFirst default + start-or-tip, FindEntry limit-1,
              AppendMessage/AppendCustomEntry via session.AppendToBranch;
              StorageBackedSession: BeginMutation grants mutation on the
              line then waits release -- CRITICAL Go fix: do NOT wait on
              the Run outcome channel (settles only after release =>
              deadlock; wait on the grant channel only); Mutate begin/
              callback/end-defer; read APIs assertOpen; GetName/GetLabel
              via stored values; FindEntries desc default + cursor seq
              bounds (asc MAX_SAFE_INTEGER, desc <=1 -> empty) + fromSeq/
              toSeq narrowing; Branch/CreateBranch (tip-exists -> Branch
              Exists, at-missing -> UnknownTarget); value/list writers via
              Mutate; SetName/SetLabel nil -> delete; Close once -> seal
              line -> storage.close -> onClose; GetBranchTip invariant;
              AppendToBranch pending-assistant check + single transaction
              entry+tip; branch name validation empty/NUL); session/
              memory.go (MemoryStorage: serialized commit queue goroutine
              (enqueue panics when closed), Commit prepare+apply, all reads
              assertOpen, Fork at serialized boundary, Close drains + idem-
              potent; MemorySessionRepo: records map + admitted set, Create
              initializes main branch, record.open tracked through OnClose
              closure (repo-level, not just admitted -- session close marks
              closed so Delete/Fork pass), Open exclusive, List, Delete
              refuses open, Fork builds destination state directly from
              the record's materialized state (source storage may be closed
              by session close; state survives) w/ parentSessionId).
              session tests: append+scan+tip+stats, pending-assistant
              rejection, branch validation (empty/NUL/dup/unknown target),
              name/label set+delete, mutate exclusive commit, tree fork
              preserves entries, open-exclusive + close-then-delete.
              P6 ROUND-12 DONE: agent/harness/session/jsonl/ — codec.go
              (ParseSessionHeader v4 (kind=header,v=4) | v3-legacy
              (type=session,version) w/ legacy id/cwd/timestamp capture;
              ParseTransaction single-object-or-array + per-write
              validation (seq>=1, entry timestamp>=0, value/list op enum);
              SerializeTransaction single-vs-array rule; committedWriteTo
              JSON field order kind->payload->seq(->timestamp) incl.
              entry/usage/value/list shapes; entry/usage/usage-cost
              fromJSON decoders; SplitCompleteLines torn-tail);
              util.go (ReadHeaderFromReader missing/unterminated/empty ->
              error, PublishFileAtomically tmp->append->rename w/ force
              remove on error, SerializeHeader key order v,kind,id,
              storageVersion,createdAt,cwd,parentSessionId?,legacyParent?,
              nextSeq?); storage.go (JsonlStorage: CreateJsonlStorage
              publishes header+initial writes then applies; OpenJsonl
              Storage header decides v4|v3-legacy; openV4 replays complete
              lines 1..n, storageVersion check, nextSeq advanceNextSeq,
              torn tail -> atomic rewrite of complete prefix; serialized
              commit queue goroutine append-before-apply; empty commit
              writes nothing; upgradeLegacyV3ToV4 prepends internal usage
              adjustment row {source:v3-import} then caller writes,
              publishes migrated file, returns seqs minus adjustment;
              withImportedUsage stats while v3-backed; IsLegacyV3;
              CaptureForkNextSeq boundary slot; Close drains); legacy_v3.
              go (ReadLegacyV3Source: header + line pass, retained types
              message/custom/custom_message/branch_summary/compaction,
              discarded model/thinking/tools changes + session_info/
              label captured as derived values, duplicate-id error,
              uuidv7(timestamp) reminting, branch tip = final retained
              entry, importedUsage = sum of message+summary usages,
              nextSeq = retained + derived + 1, mintedParent walks
              nearest retained ancestor, Writes normalized order
              entries-then-derived-values; VerifyLegacySource header
              id/cwd re-check).
              jsonl tests: header parse v4/v3/rejects + key order,
              transaction single-vs-array round-trip (6 write shapes) +
              rejects + single-object wrap, splitCompleteLines, storage
              create+reopen replay, torn-tail rewrite (torn write gone,
              complete prefix kept, file ends with newline), empty commit
              no bytes, legacy v3 open (IsLegacyV3, stats) + upgrade on
              commit (v4 header, message survived minted, name set, seqs
              skip adjustment).
              P6 ROUND-13 DONE: jsonl/repo.go (SessionDirectoryName
              --cwd-with-/-/-replaced-- (strings.NewReplacer -- Go regexp
              char-class strings from python replace silently mismatch,
              prefer explicit replacers), SessionFileName ISO ts (: and .
              -> -) + JSEncodeURIComponent id (Go QueryEscape encodes space
              as + and escapes !'()*~ -- both corrected to JS semantics),
              JsonlSessionRepo: create w/ pendingCreates reservation +
              derived path + parent-dir-creating storage + mtime metadata,
              open exclusive via openSessions map, list (cwd filter -> one
              dir; else all --dirs; only line-1 header per .jsonl; header
              cwd re-filter for the lossy dir collision; sort createdAt
              desc, id, cwd), delete refuses open, fork (open source
              storage -> CreateFork state + CaptureForkNextSeq -> write
              destination v4 file with entries AND scalars serialized as
              initial writes + parentSessionId + nextSeq high-water;
              NOTE: TS does a streaming two-pass index+copy without
              materializing -- value-equivalent, memory-profile differs,
              documented), path side-table (session metadata has no Path
              field in the Go port -- repo tracks cwd+id+createdAt->path),
              Close no-op TODO; fakeFS in tests now MkdirAlls parents
              (mirrors local env WriteFile); MEMORY: JsonlSessionRepo does
              NOT initialize branches (harness runtime does; unlike
              MemorySessionRepo which creates main) -- tests must
              CreateBranch first).
              repo tests: dir name (incl. C:\ path + documented lossy
              /a/b vs /a-b collision), file name JS encoding (%20 space,
              %2F%2B), create/open/list/delete lifecycle (dup create,
              double open, header-cwd filter), collision re-filter (two
              hand-written headers in one dir, cwd filter separates),
              same-createdAt id-asc sort, tree fork (message survives,
              parentSessionId, both sessions listed).
              P6 polish DONE (round 14): agent/harness/session/testing/
              package — StorageDecorator forwarding base (all 11 Storage
              methods), InstrumentedStorage (records commit admissions,
              clear, pass-through reads), GatingStorage (arm -> park
              commits on channels, WaitPending polls, Next FIFO release +
              await landing, Discard drops parked + rejects later with
              CommitDiscarded), StorageFixture/ConformanceCase contracts,
              CreateStorageConformance 6 groups over memory fixture
              (values set/replace/delete/recreate, list pagination desc
              cursor + atomic delete/reappend, entries parent resolution +
              duplicate id + missing parent, usage ledger totals,
              back-to-back serialization admission order, close idempotent)
              — run by a Go test runner (10 subtests).
              P7 ROUND-14 STARTED: agent/harness/runtime/ — reducer.go
              (ReduceLaneSnapshot full event table: run/compaction/
              navigation_start open operation (compaction only when idle),
              operation_abort -> aborting (matching id), run_suspend/
              resume deferred handle+poll, retry_scheduled -> {attempt,
              maxAttempts, nextAttemptAt} cleared by start/end,
              message_start only pending assistant sets streaming,
              message_update assistant-only, message_end clears,
              tool_start upsert running / tool_update fills result while
              running / tool_end settles {result,isError}, entry_added:
              toolResult removes running tool, compaction REPLACES
              transcript, tip + messageCount, queue_update, usage totals
              (cross-lane exception to lane filtering), config_update
              lane-matched model/thinkingLevel/activeTools, run_end/
              compaction_end -> lastResult record + operation null (run
              moves tip; failed carries error), navigation_end -> rebase,
              fault -> faulted; no-ops handler_error/turn_*/value_update/
              lane_created; foreign-lane events ignored except usage;
              HarnessEvent as jsonx obj w/ EventType/EventLane helpers,
              LaneSnapshot/LaneOperationSnapshot/runningTool/retry/
              deferred snapshots, Config + CompactionSettings).
              runtime tests: operation lifecycle (open/abort/foreign-id/
              end-record), compaction idle-only, streaming set/clear,
              tool lifecycle upsert/update/settle + toolResult removal,
              compaction entry replaces transcript + no messageCount bump,
              deferred suspend/resume + retry schedule/clear, foreign lane
              ignored but usage cross-lane, config updates incl. foreign
              lane, navigation_end rebase, fault + no-ops, run_end failed
              error.
              P7 ROUND-15 DONE: runtime/transcript.go (ChainEntries
              parent-chaining w/ non-mutating source, EntryLifecycleEvents
              message_start/end/entry_added vs entry_added-only, runId
              optional, CommittedEntryEvents materializes seq+timestamp
              from commit w/ firstWriteIndex offset, ReadBoundedEntries
              tip->newestFirst scan stopAtType compaction reversed to
              oldest-first + nil-tip invariant error, LaneQueuedItem +
              ReadLaneQueues pending-entry payload resolution (message vs
              custom w/ kind=write guard, missing payload invariant),
              ReadPendingMessages description-prefixed invariants);
              runtime/restore.go (ClassifiedLaneStorage absent|branch|lane
              w/ per-missing-value invariant messages, ReadLaneStorage
              three-value read, RestoreSession one-mutation scan of all
              three namespaces -> per-lane classify -> restore complete
              lanes only, RestoreLane single-lane w/ absent->missing tip +
              branch->missing config errors, RestoreLaneState resolves
              op.meta+op.state by currentOperationId w/ missing/mis-id/
              intent-mismatch invariants, StateMatchesIntent: run = not
              navigation leaf and summary only w/ resume_checkpoint
              boundary; compaction = summary + finish boundary; navigation
              unsummarized = ready_to_commit + target+label exact;
              summarized = summary + commit_navigation boundary + target+
              label + customInstructions match).
              runtime tests: classify absent/branch/partial-error/full,
              restore validates op.meta missing + matching run operation
              round-trip (At/tip/config), intent matrix run (assistant.ok
              /navigation-reject/summary-boundary), compaction (finish ok
              /resume_checkpoint reject), navigation (ready_to_commit
              target match+mismatch, summary boundary label match+mismatch
              + customInstructions), ChainEntries parent chain + source
              untouched, EntryLifecycleEvents 3-vs-1 w/ runId presence.
              P7 ROUND-16 DONE: runtime/lane.go (SelectAcceptedInbox
              write/nextRun always + steer/followUp one-at-a-time fairness;
              DurableLaneStateJSON; PendingEntryWrite message|custom; Lane
              command machine: Command waits out idleOwner (abort-signal
              Done + stateChange race), planner on the session mutation
              line -> return/reject/commit (commit: write -> swap state ->
              signal -> synchronous materialize -> events collected
              post-line via DrainEvents; idle-blocked retried; onFault
              hook), SettleOperation (commit augments op.state write +
              optional lane.state inbox write w/ durableLaneState;
              finish writes pi.result record + lane.state {current:null,
              last:opId} + clears operation + moves lastOperationId;
              planner-error => reject), ContinueOperation (control
              cancel_requested short-circuits planner -> {kind:
              cancel_requested}; return wraps {kind:result,value}); Lane
              JSON codecs operationStateToJSON/operationResultToJSON).
              lane tests: inbox fairness (always/one-at-a-time/all),
              command commit swap + materialize sees seqs, return/reject,
              settle finish clears operation + durable pi.result record
              + lastOperationId, continue cancel short-circuit (planner
              not invoked), continue result passthrough.
              P7 ROUND-17 DONE: runtime/drive.go (ProcedureResult
              continue|waiting|settled, DriveOutcome settled|waiting_retry|
              waiting_deferred, Drive {operationId,gate,context}, Drive
              Registry per-state-at injectable procedures + global
              Reconcile + BeforeDriveHook, DriveOperation loop: mismatched
              operation -> invariant, before_drive hook w/ AbortRequested
              wait, cancel_requested routes to Reconcile, per-state
              dispatch, AbortRequested error -> wait cancellation ->
              continue (abort-continue SKIPS the no-progress check -- the
              wait is progress), settled/waiting return, continue verifies
              progress by SERIALIZED state compare (struct pointers never
              value-compare; ALSO state must be snapshotted BY VALUE
              before dispatch since procedures mutate the lane's operation
              in place -- pointer views always alias) -> no-progress
              invariant).
              drive tests: settled return, waiting passthrough, chain
              starting->checkpoint->settled call order, no-progress
              invariant, cancel routes to reconcile (state procedure NOT
              invoked), operation-id mismatch, AbortRequested waits
              cancellation then settles.
              P7 ROUND-18 DONE: runtime/boundary.go (NormalizeRetryPolicy
              (enabled -> maxRetries+1 attempts, default 60s max agent
              delay), AssistantReadyAtBoundary (scope copy + fresh
              generationContext {stepId,triggerEntryId} + nextAttempt 1,
              source untouched), PlanBoundaryInbox: steer selection mode-
              aware (one-at-a-time first only), writes always selected,
              non-write kinds must be message payloads (invariant), load
              pending via pi.pending.entry, projectorSet decides custom
              projection, followUpWhenNoTrigger adds followUp (mode-aware)
              when nothing projects then restores inbox order, entries
              chained tip->... via PendingEntryWrite, selected pending
              payloads deleted, tip advanced to last entry (skip when
              empty), remainder inbox + queues snapshot via ReadLaneQueues
              when any selected, BoundaryPlacementEvents = committedEntry
              Events + optional queue_update w/ queues JSON {steering,
              followUp, nextRun?}; lane name for tip writes via SetLaneName
              holder).
              boundary tests: steer one-at-a-time vs all (entries/remainder/
              tip/chain), followUp joins only when no trigger + order
              preserved, projector custom types (unknown no-trigger /
              registered triggers), missing payload invariant, custom steer
              invariant, empty placement (tip unchanged, no trigger/queues),
              assistant.ready scope copy + source untouched,
              queue_update emission iff queues present.
              P7 ROUND-19 DONE: runtime/terminal.go (OperationCleanup
              Writes: scans + deletes op.meta/op.state + all operation-
              owned prefixes (tool_args/tool_memo/preparation/pending.
              tool_output), tools state adds pending-entry deletes for
              outcome_ready calls only (planned survive), effect_pending
              states add the pi.pending.assistant_frame LIST delete;
              OperationResultRecord: failed<->error xor invariant ("Only
              a failed operation result may carry an error"), kind from
              meta.intent.kind, fromTipId=sourceTipId, endedAt=now).
              terminal tests: error invariant both directions + failed
              with error + completed clean w/ kind/from/tip/endedAt,
              cleanup deletes all six operation-owned values while
              unrelated session.name survives (applied in one Mutate
              transaction), tools outcome_ready vs planned pending entry
              selectivity, effect_pending frame list delete empties the
              seeded list.
              P7 ROUND-20 DONE: runtime/progress.go (ReadAssistantFrames
              1000/page asc pagination w/ cursor carry; ProgressChannel
              write/seal/drain over lane commands; OpenProgress planner
              drops unless stillOwns(projection); FrameStillOwns =
              assistant|deferred effect_pending + responseEntryID match;
              ToolStillOwns = tools leaf + batch.turnId match + call
              sourceIndex+resultEntryId+effect_pending; OpenFrameProgress
              appends pi.pending.assistant_frame list; OpenToolProgress
              sets pi.pending.tool_output scalar).
              progress tests: 1500-frame pagination order across pages +
              empty list, FrameStillOwns matrix (match/wrong-entry/
              deferred/other-leaf/no-op), ToolStillOwns matrix (match/
              wrong turn/index/invocation/status, non-tools leaf),
              OpenProgress drops when ownership lost / commits when owned /
              post-seal dropped, tool output stored + frames dropped
              without matching operation.
              P7 ROUND-21 DONE: runtime/checkpoint.go (StartRun: prompt
              resolution via intent.promptEntryIds (non-run intent ->
              invariant, missing/non-message -> invariant), BeforeRunHook
              injectable (pending assistant injection -> invariant),
              reserved entries chained from tip, trigger = last entry or
              existing tip (no trigger -> invariant), commits entries +
              branch tip + checkpoint state w/ fresh need_assistant
              continuation; RunCheckpoint: threshold via injectable
              PrepareCompactionThreshold, boundary placement
              (followUpWhenNoTrigger = no-threshold && may_finish), trigger
              -> assistant.ready, threshold -> summary.deciding w/
              resume_checkpoint boundary + pi.op.preparation write +
              reason=threshold task, need_assistant w/o trigger ->
              assistant.ready at current trigger (trigger moves into
              generationContext -- assistant.ready has NO top-level
              triggerEntryId in TS), may_finish -> FinishRunBoundary;
              FinishRunBoundary: replan (followUpWhenNoTrigger always), tip
              null -> invariant, includeFinalAssistant + no latest -> "Completed
              run is missing its final assistant", OperationResultRecord
              completed + OperationCleanupWrites suffix -> finish); LANE
              FIXES (tests caught): SettleOperation commit/finish now
              propagate decision.Result into LaneCommand.Result, Command's
              commit branch sets result (was always nil), ContinueOperation
              wraps commit/finish Results into the result union like TS
              materialize; checkpoint tests need a durable root entry
              (chain parent validation).
              checkpoint tests: initial checkpoint (state/continuation/
              trigger/tip alignment), pending-assistant injection rejected,
              non-run intent rejected, missing prompt entry rejected, steer
              trigger advances to assistant.ready + inbox drained + payload
              consumed, threshold diverts to summary.deciding + preparation
              stored, may_finish settles completed + operation cleared,
              need_assistant without inbox keeps trigger in generation
              context.
              P7 REMAINING: drive generation/response + tools + deferred +
              structural decision/generation + reconcile/recovery + harness.
              go facade + agent-harness.ts exports; then P8 pico3 + chord
              tracker/services; then P9 PARITY.md.
agent/        P4+ (root pkg; harness merged pkg; tools/utils/nodejs/jsonl/
              sessiontesting/pico3 subpackages)
```

## TS -> Go mapping conventions (binding for all phases)

- One TS file -> one Go file, basename with underscores (`agent-loop.ts` ->
  `agent_loop.go`). Inside the merged `agent/harness` package, files from
  subdirectories carry a prefix (`session_*.go`, `runtime_drive_*.go`, ...).
- Identifiers: camelCase -> PascalCase; `DEFAULT_X` -> `DefaultX`.
- Discriminated unions -> sealed Go interfaces with a discriminator method +
  custom Marshal/UnmarshalJSON keyed on the TS discriminator field.
- JSON: field names and struct field ORDER mirror TS construction order
  (JSONL byte-compat); optional fields are pointers with omitempty; absent vs
  null must stay distinguishable where TS relies on it.
- Numbers are float64 (JS doubles); serialize via jsonx.FormatNumber (JS
  formatting) in hand-written codecs.
- `AbortSignal` -> `*abort.Signal` (nil == undefined). `structuredClone` ->
  `jsonx.Clone`. JS `JSON.parse/stringify` on arbitrary values -> `jsonx`.
- AsyncIterator/push-streams -> pull iterator or channel per context;
  Promise-tail serializers -> mutex+FIFO preserving admission order.
- Result<T,E> -> generic Result struct; TaggedError classes -> error structs
  with Tag(); matchError -> type switch.
- typebox `Static<T>` params -> validated jsonx values; tool schemas ->
  `*typebox.Schema`.
- Known accepted deviations (PARITY.md at P9): JsonValue map key order
  (Go sorted vs JS insertion; struct-encoded paths unaffected), pico3 Proxy
  membranes -> explicit capability APIs, CustomAgentMessages declaration
  merging -> CustomAgentMessage struct, `.?` regexes expanded.

## Porting notes per package (append as phases complete)

### P0 findings (keep for later phases)
- typebox 1.x uses hidden `~kind`/`~optional` STRING keys, not symbols;
  `Symbol.for("TypeBox.Kind")` does not exist in 1.3.27, so
  ai/utils/validation.ts's `getOwnPropertySymbols(...)` check is always false
  and the raw-JSON-Schema coercion pass ALWAYS runs. Go typebox mirrors this:
  Kind()=="" for raw schemas; validation port must always apply coercion.
- typebox Convert: string->number can yield NaN (JS Number() semantics);
  null->0; boolean->1/0; integer truncs.
- npm ignore default is case-INSENSITIVE (ignorecase option default true).
- jsdiff diffLines merges the line separator into the token; tokens for
  "a\r\nb" are ["a\r\n","b\r\n"]; CRLF separators split as ["a","\r\n",...].
- partial-json `{"a":"abc` with STR disallowed but OBJ allowed returns {} (no
  error) — the enclosing object's partial branch swallows the string error.
- jsonx.FormatNumber: JS fixed notation iff decimal exponent -6<=n<=20
  (e.g. 1e-7 -> "1e-7", 1e20 -> "100000000000000000000", 1e21 -> "1e+21").
- Reference npm tarballs extracted at /tmp/pi-npm-ref (typebox-1.3.27,
  diff-8.0.4, ignore-7.0.8, partial-json-0.1.7) for future byte-comparisons.
  Regenerate with: `cd /tmp/pi-npm-ref && npm pack <pkg>` if lost.
- Node 26 available for golden generation: run the actual JS packages to
  produce expected outputs for Go tests.

## Round 10 — P3b providers (2026-09-28): anthropic/openai/openai-codex/deepseek/zai/zai-coding-cn/openrouter

User-scoped subset of `pi/packages/ai/src/providers` (7 of 46). All new code in package `ai`, file prefixes `api_*` / `auth_*` / `provider_*` / `catalog_*` (merged-package convention).

### What landed

- `ai/catalog/data/` — snapshot of pi's generated model catalog (42 shards +
  `.manifest.json`, gitignored upstream, produced by
  `node scripts/generate-models.ts` non-strict; models.dev + openrouter.ai
  reachable). `catalog_data.go` embeds + validates (schemaVersion 6, sha256
  per shard) + flattens chat models (model-catalog.ts port).
- API clients speaking the wire protocol directly (TS used the
  @anthropic-ai/sdk 0.124.0 / openai 6.40.0 SDKs; SDK references unpacked at
  /tmp/sdk-ref):
  - `api_anthropic_messages.go` + `_stream.go`: POST
    `{base}/v1/messages?beta=true`, anthropic-version header, x-api-key vs
    Bearer (oauth-token/copilot), stealth Claude-Code tool naming for oauth
    tokens, beta feature assembly, budget vs adaptive thinking, thinking
    signatures/redacted, fine-grained tool streaming, mid-conversation
    system/tool-change blocks (tool_addition/tool_removal + deferred
    placeholder), cache_control incl. 1h ttl + 2x write pricing, fallback
    models with per-response cost override, input_transformations
    diagnostics, mapStopReason incl. refusal/pause_turn/sensitive.
  - `api_openai_completions.go` + `_stream.go`: compat resolution
    (detectCompat from provider/baseUrl merged with catalog compat),
    thinkingFormat matrix (zai/qwen/chat-template/baseten/deepseek/
    openrouter/ant-ling/together/string-thinking/openai), reasoning_details
    delta merging into the signature slot, reasoning-content replay on
    assistant messages (deepseek), grammar custom tools with input JSON
    buffering, anthropic cache_control on openrouter anthropic/* models,
    prompt_cache_key/retention, openrouter provider routing, usage parsing
    (cached/cache_write split; input = prompt - cacheRead - cacheWrite).
  - `api_openai_responses.go` + `_shared.go` + `_stream_shared.go`:
    convertResponsesMessages (msg/fc item ids + TextSignatureV1 envelope,
    foreign fc_ id rebuilds, tool_search/additional_tools),
    processResponsesStream (slots keyed by output_index, reasoning item
    replay via signature, encrypted_content backfill from the terminal
    response, refusal deltas, service-tier pricing multipliers, incomplete
    status mapping incl. max_output_tokens → length).
  - `api_openai_codex_responses.go`: /codex/responses URL resolution, JWT
    chatgpt-account-id extraction, originator/OpenAI-Beta/session headers,
    codex retry policy (429/5xx + text patterns, terminal usage-limit
    detection, friendly "You have hit your ChatGPT usage limit" messages),
    header-phase timeout, response.done → response.completed normalization
    with end_turn capture.
- Infrastructure: `auth_provider.go` (env-key auth + credential store +
  resolveProviderAuth + anthropic AUTH_TOKEN Bearer), `env_api_keys.go`,
  `provider_retry.go` (x-should-retry/retry-after with cap + abortable
  sleep), `error_body.go` (+ WrapStainlessHTTPError: SDK message is
  `<status> <JSON.stringify(parsed body)>` — verified against both SDK
  sources), `http_sse.go` (FetchFunction seam; DefaultFetch keeps the
  request context alive via cancelOnCloseBody until the body closes),
  `provider_core.go` (createProvider with single/by-API dispatch +
  deferred upgrade), `api_simple_options.go`, `api_constrained_sampling.go`,
  `api_transform_messages.go`, `api_copilot_headers.go`, `jsonx_access.go`.
- Registry: modelsImpl.applyAuth (auth resolution → apiKey/headers/env
  merge → model baseUrl override → dispatch); `providers_all.go` with the
  7 provider definitions + BuiltinProviders/BuiltinModels.
- Tests: httptest SSE replay suites per client (event mapping, usage+cost,
  thinking modes, tool streams, error bodies, mid-stream abort, retry,
  request shapes incl. cache_control/betas/effort/verbosity), provider
  registry + catalog decode suites, TransformMessages suite,
  `providers_smoke_test.go` (live 1-token streams only when
  PI_SMOKE_TESTS=1 AND the provider key is exported).

### Deviations (documented)

- OAuth interactive login/refresh flows NOT ported (auth/oauth/* ~2.6k
  lines): ProviderOAuthAuth advertises the slot; resolving a stored OAuth
  credential returns ModelsError oauth. openai-codex is OAuth-only in pi —
  this port resolves its ChatGPT token from OPENAI_CODEX_API_KEY.
- Codex WebSocket transport (connection cache, continuation state,
  connection-limit retry, ws beta header) NOT ported: `transport`
  auto/websocket-cached behave as SSE. zstd request compression omitted
  (backend accepts uncompressed JSON).
- pi-user-agent omits the kernel-release segment ("pi (darwin; amd64)").
- sanitize-unicode strips invalid UTF-8 (Go strings cannot hold unpaired
  surrogates).
- Error display follows the SDK path exactly: `<status> <stringified
  body>`, which composes with formatProviderError as tested.
- Catalog data for the other 39 providers is embedded but their provider
  definitions/APIs (bedrock SigV4, google, mistral, system-one, radius
  pi-messages, openrouter-images/classifiers) are future work.

## CA1 ROUND-1 DONE (tui foundation): colors, terminal-colors, Unicode core, utils

- tui/colors.go: full colors.ts port — IndexedColor/RgbColorValue/OklchColorValue
  sealed Color union; parseColor hex-3/6 + oklch(NUM %? NUM NUM deg?) regexes;
  Oklab<->linear-sRGB matrices; oklchToRgb gamut mapping w/ 20-step chroma
  bisection; colorToOklch; mixColors oklch-hue-shortest-path + srgb space;
  rgbToAnsi256 (cube + grayscale distance rule); styleText/styleTextWithAnsi
  reverse-order resets. GOLDEN: 3 test groups from colors.test.ts incl. exact
  ANSI byte sequences.
- tui/terminal_colors.go: OSC 10/11 reply parser (BEL/ST, hex 6/12, rgb:r/g/b
  channels of any hex length) + ?997 scheme report; parse-only tests from
  terminal-colors.test.ts (TUI query tests land with TuiMainScreen).
- tui/unicode_data.go (GENERATED by scripts/gen-unicode-tables.mjs from UCD
  16.0.0: GraphemeBreakProperty, DerivedCoreProperties InCB L/C/E, emoji-data
  Extended_Pictographic/Emoji_Presentation/Modifier(+Base), emoji-sequences +
  emoji-zwj-sequences RGI string sets — 3.7K sequences embedded; digests
  pinned). Unicode 16.0.0 = Go 1.27 stdlib tables = Node 26 ICU (the TS
  reference runtime).
- tui/grapheme.go: UAX #29 extended grapheme cluster segmenter — full rule
  set GB1-GB999 incl. GB9c (InCB Linker/Consonant/Extend chain state), GB11
  (pict ZWJ arming state machine), GB12/13 RI parity, Hangul L/V/T/LV/LVT
  tables; isRGIEmoji = exact UCD sequence-set lookups. GOLDEN: 79-case battery
  generated from Node Intl.Segmenter (Devanagari conjuncts, ZWJ families, RI
  runs incl. invalid AA pair, keycaps, tag flags, Hangul jamo, CRLF, Thai) —
  all byte-identical.
- tui/utils.go: full utils.ts port — visibleWidth (ASCII fast path + FIFO
  width cache 512), graphemeWidth (terminal spacing marks incl. Mc-minus-
  exceptions + legacy list, zero-width clusters, couldBeEmoji prefilter,
  RI=2 conservative, trailing InCB/FF-width/AM accounting), truncateToWidth
  (contiguous-prefix rule, clipped wide ellipsis, pending-ANSI attachment,
  OSC-8 close-before-ellipsis), truncateFragmentToWidth, wrapTextWithAnsi
  (token split w/ per-CJK-grapheme tokens, ansiCodeTracker SGR state incl.
  38/48;5|N and ;2;R;G;B multi-param consumption, line-end underline-only
  reset + hyperlink reopen w/ BEL terminator preservation, breakLongWord),
  sliceByColumn/sliceWithWidth strict, extractSegments with style inheritance,
  stripTerminalSequences, normalizeTerminalOutput (Thai/Lao AM decompose,
  tabs-in-escapes byte-identical), extractAnsiCode CSI/OSC/APC, OSC 8
  open/close/active tracking, getGraphemeCellRange, getOsc8LinkAtColumn.
- tui/east_asian_width_data.go (GENERATED from npm get-east-asian-width 1.6.0
  lookup-data.js — same tables, exact parity; eastAsianWidth = 2 iff Fullwidth
  or Wide).
- tui/jsmath.go: jsRound (half toward +inf incl. -0), jsPow, jsMod,
  formatJSNumber via jsonx.FormatNumber.
- utils tests ported: truncate-to-width (10 cases incl. BEL OSC-8 label
  truncation + contiguous-prefix), visibleWidth battery (Hindi/Bengali/Gujarati/
  Oriya/Telugu/Malayalam conjuncts, Myanmar marks incl. ကို=1, Thai/Lao AM,
  CJK/kana, combining zero-width, OSC-133), wrap-ansi (underline/background/
  CJK/color-continuation/OSC-8 reopen+close+BEL preservation), tab-width
  pure subset (sliceWithWidth/extractSegments/normalize control-sequence
  tabs). ONE TS->GO trap: `segment.length === 1` is UTF-16 units — Go byte
  len() broke CJK tokenization (中文… split as one 41-wide token); use
  []rune(segment) length. All 5 test files green.

## CA1 ROUND-2 DONE (tui keys): full keys.ts port

- tui/keys.go: MatchesKey/ParseKey/IsKeyRelease/IsKeyRepeat/DecodeKitty
  Printable/DecodePrintableKey/Set|IsKittyProtocolActive + KeyId constants
  (Key* block mirroring the TS Key helper object) and KeyCtrl/KeyShift/...
  modifier constructors. Kitty CSI-u parsing (codepoint:shifted:base;
  mod:event u incl. empty-shifted ::base form, arrow 1;<mod>[event][ABCD],
  functional <num>[;mod][:event]~, home/end 1;<mod>[event][HF]) with
  KITTY_FUNCTIONAL_KEY_EQUIVALENTS keypad normalization (KP_0..KP_DELETE),
  normalizeShiftedLetterIdentity (lock-mask stripped, shift+A-Z -> a-z);
  base-layout-key fallback gated on non-Latin/non-symbol codepoints
  (Dvorak/Colemak/xremap false-match guard); xterm modifyOtherKeys
  CSI 27;mod;keycode~ incl. printable decode (shift-only); legacy tables
  (LEGACY/SHIFT/CTRL sequences + LEGACY_SEQUENCE_KEY_IDS incl. rxvt $/^
  forms, alt-arrows ESC b/f/p/n); raw 0x08 ambiguity via WT_SESSION + SSH_*
  env; mode-dependent interpretation (\r/\n = shift+enter when kitty
  active, alt+* legacy ESC-prefixes disabled when active); rawCtrlChar
  code&0x1f (letters + [\\]_ and - -> ^_).
- keys tests: full keys.test.ts port (633 lines) — kitty alternate-key block
  (Cyrillic ctrl+c/d/z, ctrl+shift+p, Dvorak codepoint-preference both
  directions, wrong-key/wrong-modifier negatives, event-type + full-format
  sequences, super/super-combined bindings, digit CSI-u, keypad
  normalization battery), modifyOtherKeys block (letters/enter/tab/
  backspace/escape/space/symbol/digit/shifted-uppercase/ctrl+alt both
  formats), legacy block (ctrl bytes, linefeed-mode switch, ctrl+symbol,
  ctrl+alt+symbol, WT_SESSION x3 env scenarios via stashEnv, alt-prefixed
  sequences on/off kitty mode, arrows/SS3/xterm-viewport/f-keys/rxvt),
  decodeKittyPrintable (incl. nav keys not printable), parseKey blocks.
  ALL GREEN with zero deviations found.

## CA1 ROUND-3 DONE (tui word segmentation + input plumbing)

- Generator extended: UCD WordBreakProperty.txt (18 wbt* tables). NEW GOLDEN
  pipeline: word segmentation goldens generated from Node Intl.Segmenter
  (62 cases incl. contractions, 1,456.789, snake_case, Hebrew quotes,
  WSegSpace runs, RI pairs, ZWJ emoji, leading combining marks, NBSP-vs-space,
  CR/LF/NEL/LS/PS). DOCUMENTED DEVIATION: ICU CLDR dictionary tailoring for
  CJK ideographs/Hiragana (locale-dependent; 3 golden cases excluded;
  pi's own word-navigation CJK expectations adapted) — default UAX #29 rules
  segment per-char, matching small-icu Node builds; isWordLike additionally
  true for letter-category segments so CJK steps per-char in word navigation
  instead of skipping runs as punctuation.
- tui/word_segmenter.go: UAX #29 word boundaries WB1-WB999 incl. WB4
  ignore-rule units (Extend|Format|ZWJ attach; lone leading mark its own
  segment per Intl goldens), WB3c ZWJ×ExtPict, WB3d WSegSpace, WB6/7/11/12
  lookahead, WB7a-c Hebrew quotes, WB15/16 RI parity.
- tui/word_navigation.go: FindWordBackward/Forward w/ injectable segment fn +
  atomic-segment predicate (paste markers); punctuation-boundary handling
  inside word-like segments; rune-offset cursor semantics.
- tui/fuzzy.go: fuzzyMatch scoring (consecutive x5 rewards, gap x2 penalties,
  word-boundary -10, position x0.1, exact -100, alpha<->digit swap fallback
  +5) + generic fuzzyFilter (whitespace/slash tokens all-match, stable score
  sort). kill_ring.go + undo_stack.go (injectable clone replaces
  structuredClone; EditorSnapshot owns its deep copy).
- tui/stdin_buffer.go: StdinBuffer full port — sequence completeness (CSI
  final-byte 0x40-0x7E, SGR mouse pattern validation, old-style ESC[M+3,
  OSC/DCS/APC ST-or-BEL), WezTerm ESC+ESC+CSI split, kitty printable
  dedup (drop raw dup rune after CSI-u printable), bracketed paste re-wrap,
  lone-ESC escape timeout vs sequence timeout, collect-then-emit locking.
  Full test suite ported incl. delayed mouse chunks + timeout behaviors.
  TEST-FIX: reference's "function key" case is \x1b[11~ (not \x1b[OP which
  CSI-parses as complete at 3 bytes — reference behaves identically).
- tui/terminal.go: Terminal interface + ProcessTerminal — termios raw mode
  via syscall (darwin TIOCGETA/SETA, linux TCGETS/SETS, other platforms
  fail loud stub; NO new dependency), SIGWINCH resize watch (nil-signal on
  non-unix), kitty negotiation state machine (flags query \x1b[>7u + DA
  sentinel, split-response reassembly buffer + 150ms flush, modifyOtherKeys
  fallback \x1b[>4;2m/\x1b[>4;0m, \x1b[<u teardown), StdinBuffer wiring,
  paste re-wrap to handler, DrainInput (idle-window stdin consumption w/
  watcher channel), OSC 9;4 progress keepalive, PI_TUI_WRITE_LOG log,
  bracketed paste enable/disable, COLUMNS/LINES fallback sizing,
  refreshTerminalDimensions (self-SIGWINCH), Apple-Terminal shift+enter
  normalization (native modifier query = documented deviation: off).
  writeOut seam for tests. Tests: negotiation harness (7 cases: query-first,
  non-zero flags, zero-flags fallback, DA fallback, normal-input forwarding,
  split confirmation, buffered CSI-prefix replay after fragment timeout) +
  ResolveEscapeTimeoutMs battery + normalize functions.

## CA1 ROUND-4 DONE (tui terminal-image)

- tui/terminal_image.go: full terminal-image.ts port — capability detection
  env matrix (tmux probe w/ 250ms exec timeout + client_termfeatures, tmux/
  screen hyperlink rules, kitty/ghostty/wezterm/warp kitty-graphics, iterm2,
  WT_SESSION, alacritty/vscode/zed, jetbrains, windows console, conservative
  unknown default), PI_HYPERLINKS/PI_IMAGE_PROTOCOL/PI_TRUE_COLOR overrides
  (hyperlink override bypasses tmux probe), cached capabilities + programmatic
  overrides (pointer-field partial), cell dimensions, IsImageLine (prefix
  contains), AllocateImageID [1,0xffffffff], EncodeKitty (4096 chunking
  m=1/m=0, a=T,f=100,q=2, C=1 moveCursor opt-out, c/r/i params), delete
  commands (d=I/d=A/d=a), EncodeITerm2 (inline/size=decodedBytes/width/
  height/name=b64/preserveAspectRatio), kitty placement metadata registry
  (FIFO eviction at 1000 w/ insertion order list — Go maps are unordered),
  GetKittyImagePlacement (chunked-transmission walk, placement-control-key
  filter -> a=p,q=2 command + replacement line), CropKittyImageLine (y/h/r
  rewrite w/ floor/ceil row math), CalculateImageCellSize (min-scale fit +
  chooseLessDistortedCellCount aspect optimization), PNG/JPEG/GIF/WebP
  dimension sniffers (IHDR, SOF C0-C2 marker walk, logical screen, VP8/VP8L/
  VP8X), RenderImage dispatch, Hyperlink OSC-8, ImageFallback (~/ shortening,
  file:// links when hyperlinks+absolute; Windows drive-path absoluteness
  check fixed for precedence).
- Tests: isImageLine battery (TS negatives are MISSING-ESC texts — partial
  ESC-prefixed sequences DO match in the reference too), capability env
  matrix + override matrix (probe bypass, programmatic set/reset),
  encodeITerm2 size=3 exact, encodeKitty C=1 + delete commands, thin-image
  r=1 + ceiling-placement r=5 sizing, imageFallback 4 cases (incl. OSC-8
  visible-text strip), hyperlink wrapping. GO TRAPS: byte shift widths need
  uint32() casts; JS || precedence vs Go (drive-letter check); TS test's
  "partial sequence" negatives differ from intuition.

## CA1 ROUND-5 DONE (tui core: TuiBase)

- tui/tui.go + tui/tui_base.go: full tui.ts port —
  - Mouse model: TuiMouseEvent (local/absolute coords, modifiers,
    wheelDelta/clickCount), TuiMouseEventResult (handled/capture/focus/
    render), DispatchMouseEvent (target + focus-target capture), Retarget.
  - Component as Go interface + OPTIONAL CAPABILITY INTERFACES (InputHandler/
    MouseHandler/WantsKeyReleaseComponent/Focusable via SetFocused/IsFocused)
    replacing TS optional methods; Container (vertical stack w/ cached mouse
    layout; InputHandlerOverride hook stands in for TS subclasses defining
    handleInput as the container-focus condition).
  - CURSOR_MARKER APC, CompositeTuiLine (extract/slice/pad + SEGMENT_RESET
    brackets, width-guarded), TuiMode, TuiStopOptions, TUI interface,
    ViewportTUI marker + IsViewportTUI.
  - TuiBase: embeddable struct w/ DoRender/ResetRenderState/Before/After
    start/stop hooks + injectable Now/After clocks (screens install them).
    Focus engine: setFocusInternal with overlay-restore policy (eligible/
    blocked states, blockedBy remount checks, focus-target vs restore-overlay
    resumes, overlay-focus-ancestor walk). Overlay stack: ShowOverlay handle
    (hide/setHidden/focus/unfocus w/ explicit target, bounds), HideOverlay,
    retarget preFocus on removal, visible() callback + nonCapturing rules,
    topmost-visible-by-focusOrder. Render scheduler: 16ms throttle,
    requestRender nextTick vs requestImmediateRender preempting queued
    timers, renderNow(force). Input pipeline: OSC 10/11 reply accounting
    (separate reply counters so late replies still consumed after timeout),
    ?997 scheme report fanout, input-listener chain (consume/rewrite), CSI 16
    t cell-size consumption + invalidate, shift+ctrl+d debug hook, invisible-
    focused-overlay redirect, eligible/blocked restore resolution, key
    release filtering w/ WantsKeyRelease opt-in, immediate render after input.
    Overlay layout: margins/uniform, width min 80-default + minWidth clamp,
    maxHeight clamp, anchor/row/col percent-or-absolute + offsets + bounds
    clamp, CompositeOverlays (focusOrder stable sort, two-pass sizing,
    viewportStart padding — no high-water mark, defensive width truncation),
    ApplyLineResets, ExtractCursorPosition (bottom-viewport scan + strip).
    Queries: QueryTerminal{Background,Foreground,ColorScheme} as buffered
    channels; ?2031 notification toggle.
  - GO API ADJUSTMENTS (documented): scheme listeners + input-listener
    removal use id-registry/disposer (funcs not comparable;
    removeInputListener has zero reference callers — disposer is the only
    removal path per repo registration convention); container-focus for
    subclass containers via InputHandlerOverride instead of runtime
    handleInput detection.
- tui/tui_test.go: TestTerminal fake + minimalScreen stub (doRender =
    render+composite+resets) — ported the TUI-level terminal-colors.test.ts
    suite (OSC 11 hex reply, reply-consumed-before-listeners, unparseable
    strict reply -> nil, non-matching input dispatched during wait, OSC 10/11
    no cross-talk + nil component input) PLUS listener consume/rewrite chain,
    key-release filter + opt-in, overlay focus lifecycle (focus/hide restore/
    IsFocused/HasOverlay/bounds-after-hide), overlay bounds + compositing at
    row/col w/ base line preserved, non-capturing overlay keeps focus,
    compositeTuiLine basics, ExtractCursorPosition in/out of viewport.
    13/13 GREEN.

## CA1 ROUND-6 DONE (tui main screen + render tests)

- tui/tui_main_screen.go: full tui-main-screen.ts port —
  BoundedTerminalWriter (1MiB chunked writes; TS counts UTF-16 chars, Go
  chunks UTF-8 bytes — no surrogate concern), kitty image header parsing
  (ids/rows from \x1b_G params), TuiMainScreen: capture/restore render state
  (image lines restore blank), beforeTerminalStop newline-after-content
  (skip w/ preserveScreen), reserved-rows walk (blank lines below an image
  line), changed-range expansion for kitty image blocks, delete-changed-ids,
  doRender full pipeline: first render (no clear), width/height full redraw
  (height skipped under TERMUX_VERSION), clearOnShrink full redraw (no
  overlays), first/last changed detection + appendStart, deleted-lines path
  (viewport-up + extraLines>height fallbacks, \r\x1b[2K clear walk),
  firstChanged<viewportTop fallback, scroll-to-append (moveToBottom + CRLF
  scroll accounting), differential CUU/CUD + \r + changed-lines-only w/
  2K-per-line, kitty reserved-rows pre-clear w/ would-scroll fallback + image
  cursor-up/down placement, overshoot line clear + move-back, crash dump on
  line exceeding width (log to LogDirectory or tempdir, stop, panic w/ TS
  message), positionHardwareCursor (clamp, row delta, 1-indexed column,
  show/hide by setting). Viewport protocol stubs (SetLayoutRoot no-op).
- tui/virtual_terminal_test.go: test-only VT emulator replacing
  @xterm/headless — grid w/ scrollback, auto-wrap, CR/LF/CUU/CUD/CHC/EL(J
  0/1/2)/ED(2/3)/CUP, OSC/APC zero-width skip, sync/paste/cursor-visibility
  ignored, resize, viewport/scrollbuffer/cursor queries, trailing-space
  trim (translateToString(true)). NUL-cell rendering fixed to spaces.
- tui/render_test.go: ported tui-render.test.ts core — input preempts
  throttled frame (renderCount exact), height-change full redraw + Termux
  skip (no 2J/3J, Line 19 stays visible through 4 resizes), width-change
  redraw, shrink-to-2/single/empty clears rows below, middle-line spinner
  update, first-line update, appended lines scroll into view, style resets
  per line. 14/14 GREEN (Kitty image cleanup tests land with Image
  component).

## CA1 ROUND-7 DONE (tui keybindings + layout engine + stack/scroll/text)

- tui/keybindings.go: full keybindings.ts port — TUIKeybindings (44
  definitions incl. editor/input/select/altScreen sets with TS shadowing
  comments), KeybindingsManager (user-claims conflict detection, defaults-
  vs-user resolution w/ normalizeKeys dedup, Matches/GetKeys/GetDefinition/
  GetConflicts/SetUserBindings/GetUserBindings/GetResolvedBindings), global
  Set/GetKeybindings. Definition iteration is sorted (Go map unordered; TS
  insertion order only observable in map key order — no behavior depends on
  it). Mutex-guarded.
- tui/layout_node.go: Symbol-keyed method -> LayoutComponent interface;
  StackBasis (auto sentinel), ScrollLayoutState interface.
- tui/stack.go: Stack w/ AddChild(component, options)/RemoveChild/Clear
  entries; VStack.Render (visible entries, gap rows, slice-to-size +
  min-size padding) and HStack.Render (intrinsic widths, allocated widths,
  CompositeTuiLine side-by-side w/ cross-axis align) — the TS subclasses'
  overrides, found BY TEST (Container concatenation alone breaks gap/min-size
  expectations); AllocateStackSizes (clamp to min/max, grow/shrink weighted
  distribution loop w/ capacity + progress guards).
- tui/scroll_view.go: ScrollView full port — follow-end semantics (suppressed
  at end, re-follow on updateLayout), ScrollTo/ScrollBy (returns unused
  delta = overscroll), ScrollToStart/End, transient auto-scrollbar
  visibility w/ hide timer + activity marking, always-scrollbar column
  reservation (getContentWidth, render pads column), single-child guards
  (panic), LayoutNode.
- tui/layout.go: layout engine — layoutComponent (plain box w/ CURSOR_MARKER
  line-offset tracking, scroll branch w/ updateLayout + translateBox +
  primary scroll view election, vstack/hstack branches w/ basis-vs-measured
  intrinsics + stretch/center/end alignment + zero-width passthrough),
  renderCache (NOTE: Component map keys — value types holding slices are
  unhashable; all real components are pointer types, tests use pointers),
  replaceScrollbarCell (grapheme-cell replacement preserving target bg for
  non-always scrollbars), GetScrollbarGeometry (thumb math, auto-reveal),
  paintBox (clip-window painting, OSC-133 zone strip, kitty image bottom-
  boundary crop + scrolled-off re-anchor, full-width fast path),
  RenderLayoutFrame, GetLayoutBoxesAt (layer-then-depth hit path),
  GetScrollViewBox, GetScrollViewsAt (deepest first).
- tui/text.go: Text (word wrap + margins + optional bg padding + vertical
  padding + cache), TruncatedText (first line + truncate + pad), Spacer.
- Layout tests (13 ported + verified): vstack grow [1,3] exact lines,
  scroll-not-rendered-during-stack-measurement (renderCount == 1),
  1e9-line scroll content clipped painting, shrink-to-min [1,3], nested
  minimum sizes (8-row dock w/ minSize padding), gaps around invisible
  entries (via VStack.Render), kitty image boundary crop (y=0,h=34,r=1),
  horizontal compose "left  right", zero-width child skipped, follow-end +
  unused delta sequence (-2/-3/+10), always-scrollbar column reservation,
  thumb heights 19/10/4/2, transient hide after delay, follow-end growth
  without scrollbar. GO TRAP: value-type components panic as map keys.

## CA1 ROUND-8 DONE (tui alt-screen search + flash; test determinism)

- tui/alt_screen_search.go: alt-screen-search.ts INDEX half — buildSearch
  Corpus (ASCII fast path whole-run spans w/ linearColumns; grapheme path w/
  visible-width columns; whitespace collapses to single separators both
  within and across rows), normalizeQuery (whitespace collapse + trim),
  escapeRegExp, findSearchCorpusMatches (global case-insensitive literal,
  span-walk w/ monotonic spanIndex, linear ASCII column interpolation vs
  grapheme whole-span columns, same-row segment merging), AltScreenSearch
  Index (corpus + match caching keyed by lines identity then normalized
  query), FindAltScreenSearchMatches, GetAltScreenSearchMatchKey.
  PLUS components/alt-screen-flash.ts: AltScreenFlashContainer (id-keyed
  entries, per-entry hide timers via injectable After, inverse-video
  truncated render, dispose). The AltScreenSearchComponent (Input-dependent
  overlay UI) lands with the components batch.
- Tests: normalized cross-row match (alpha QUICK/brown fox exact segments),
  ASCII+Unicode column mapping (ANSI-stripped "foo  bar" + "A界🙂éZ" →
  segments [1,3)+[5,8)+[0,6)), index reuse (changed/cache/query-change/
  line-change), flash container render/truncate/dispose. 4/4 GREEN.
- TEST DETERMINISM FIX: StdinBuffer timing tests converted to a manual
  timer clock (After seam on the buffer) — fireFirst() simulates elapsed
  timeouts instead of sleeping; the ESC+CR merge case deliberately does NOT
  fire (CR arrives before the 100ms window). Capability-override test now
  resets package overrides for -count reruns (state persists across reruns
  in one process). Suite stable across repeated count=3 stress runs; one
  rare load-dependent flake remains under extreme contention (negotiation
  replay sleeps) — queued for deterministic conversion next round.

## CA1 ROUND-9 DONE (tui alt screen: the 1745-line renderer)

- tui/tui_alt_screen.go + tui_alt_screen2.go: full tui-alt-screen.ts port:
  - Screen lifecycle: enter/exit alt buffer (?1049), autowrap off, mouse
    tracking mode selection (button-motion under TMUX/ZELLIJ/STY/tmux-/screen-
    TERM prefixes, all-motion otherwise), 2J+home+cursor-hide entry; stop
    path replays the document (OSC-133 zones stripped, CURSOR_MARKER removed,
    width-clamped) into the main screen inside one synchronized-output block,
    or preserves the screen when requested; iTerm2 images disabled for the
    fullscreen session w/ capability save/restore.
  - Kitty image management: placement cache w/ insertion-order list (Go map
    unordered), same-generation replacement lines, offscreen eviction
    (16 images / 32MiB transmission / 64MiB decoded budgets), full-redraw
    clear (d=a placements when uploads exist else d=A), WezTerm
    clear-then-draw separation (WEZTERM_PANE/TERM_PROGRAM detection).
  - Viewport input: FOCUS_IN/OUT focus-loss reset (selection gesture,
    component clicks, hover, drag), wheel routing (SGR + legacy ESC[M
    parsing, Alt 5x multiplier, overlay-first dispatch, scroll-view stack
    w/ overscroll chain-until-contain + primary fallback), SGR mouse
    handling (press/drag/move/release typing, capture routing, component
    click counts w/ triple-click cycling), keybinding navigation (page/
    half-page/line/prompt-jump via OSC-133 anchors/top/bottom/search w/
    release-event filtering and overlay-deferral), right-click paste
    (Windows-only, VS Code excluded).
  - Selection model: scroll-aware points (content rows vs screen rows),
    word selection joining path/kebab tokens across word segments, line
    selection, click counts (500ms window, same-word restart), anchor/focus
    semantics w/ initial-range pivot on drag, auto-scroll ticker at 50ms w/
    boundary detection and stop-on-content-edge, OSC-8 URL click activation
    (press records, release activates when undragged), copy-on-select w/
    injected clipboard or OSC 52 fallback + "Copied!"/error flash.
  - Scrollbar interaction: hover reveal (hidden auto tracks), thumb drag w/
    grab offset, track-click jump-to-middle, geometry recompute per event.
  - Transcript search: toggle/close overlay (top-right 40% min 32 margin 1),
    query update preserving anchor, next/previous w/ selected-key identity
    across re-indexes, binary-search anchor reveal at 1/3 viewport, match
    highlight compositing (row/column ranges, current vs other styling,
    scrollbar column exclusion, right-to-left slice application).
  - doRender pipeline: layout frame (re-rendered when search scrolls), OSC-
    133 strip, search highlights, jump-to-end indicator (follow-end primary
    only, centered on last row, clickable rect recorded), overlays, bottom
    height-window clamp, selection inverse-video, flashes (right-aligned),
    cursor extraction + width clamp, absolute CUP+EL row writes (skipping
    unchanged rows), hardware cursor positioning, synchronized output.
- tui/alt_screen_search_component.go: search overlay UI (boxed 3-line
  render, dim placeholder, result counter, ↑/↓ buttons w/ key labels incl.
  darwin Alt->Option, narrow-width collapse to bare arrows, hover states,
  button hit boxes). DEVIATION (interim): embeds a minimal single-line
  editor behind an Input seam; the full Input component port replaces the
  internals without contract change (noted for PARITY).
- DEADLOCKS FOUND + FIXED (3): Stop-hook replaying the document re-locked
  the base mutex (renderUnlocked path); input listeners ran under the base
  lock while the alt-screen listener re-locks (listeners now run unlocked
  w/ consume propagation); ScrollView's render callback fires both locked
  (input path) and unlocked (layout path) — scheduling is now async like
  the reference's nextTick.
- Tests: alt screen smoke (enter buffer, follow-end shows line 11), scroll
  input (single pageUp = viewportHeight-4 lines reaches top on height 10;
  pageDown returns to end), exit replay (document lands in main screen
  scrollback). Full suite + 3x count=2 stress green.

## CA1 ROUND-10 DONE (tui components batch: box/mouse-region/loaders/input/select/settings/image)

- tui/box_loader.go: Box (padding + bg w/ bg-sample change detection cache,
  child mouse dispatch at padding-adjusted coords), MouseRegion (child-first
  dispatch then region handler), Loader (Text-based spinner w/ frames 80ms
  default, verbatim-vs-colored indicator, message updates, async ticker),
  CancellableLoader (Escape via tui.select.cancel -> abort.Controller from
  the ported abort/ shim package), EditorComponent interface (typed as
  required core + optional capability interfaces: HistoryAdder/
  TextInserter/ExpandedTextGetter/AutocompleteProviderSetter/BorderColor/
  PaddingX/AutocompleteMaxVisible — TS optional members become Go optional
  interfaces).
- tui/image_select.go: Image (Kitty ID allocation on first render, row
  reservation, iTerm2 cursor-up placement, theme-colored fallback w/
  sniffed-then-default 800x600 dims), SelectList (prefix-filter + wrap-
  around nav + confirm/cancel keybindings, centered visible window, primary
  column min/max bounds w/ widest-label measurement, description alignment
  + truncation incl. custom truncatePrimary hook, scroll indicator, wheel +
  press/click mouse w/ press-then-click pairing and hover-does-not-select).
- tui/input.go: Input — single-line editor w/ horizontal scrolling (cursor
  centered when mid, end-reserved column, renderedStartColumn tracking for
  mouse clicks), placeholder w/ dim styling + inverse-video first grapheme,
  bracketed paste buffering (start/end markers, recursion for trailing
  input, newline stripping + tab expansion), kill ring (Ctrl+W/U/K w/
  accumulate-on-consecutive-kill semantics, prepend-backward/append-forward,
  yank + yank-pop rotation guards), undo (type-word coalescing: whitespace
  pushes its own group — TS parity confirmed against the reference test),
  grapheme-aware cursor/backspace/forward-delete, word navigation via byte-
  <->rune index conversion (BUG FIXED: cursor is a byte offset; word moves
  must convert rune positions back to byte offsets), Kitty CSI-u printable
  decode before control-char rejection (C0/DEL/C1), InputOptions.Prompt as
  *string (TS undefined-vs-"" distinction), focus marker + inverse cursor.
- tui/settings_list.go: SettingsList — search input row (spaces feed the
  query once text exists; bare Space cycles before any query), value
  cycling, submenu open/close w/ navigateTo auto-reopen and selection
  restore, fuzzy label filter, description wrapping + hints, mouse routing
  (submenu > search row 0 > blank row 1 > list rows w/ 2-row offset).
- Tests: input.test.ts port (submit-backslash, placeholder, CJK/fullwidth
  no-overflow at 3 cursor positions, scroll-keeps-cursor, kill ring battery:
  W/Y, punctuation, Unicode per-char (documented deviation — expectations
  adapted like word-navigation), U/K, empty-ring, yank-pop cycling + guards,
  accumulation + breakers, Alt+D forward, undo coalescing), select-list
  (multiline description normalization, alignment under truncation, min/max
  column bounds 14/22, custom truncation w/ …), settings-list (search-space
  vs shortcut-space, enter cycles), truncated-text battery (pad/vertical/
  truncate/ANSI/newline/empty). 25/25 GREEN. Input fix found 2 REAL BUGS:
  word-move byte-offset arithmetic, Prompt default semantics.

## CA1 ROUND-11 DONE (tui latex: the 1506-line Unicode math renderer)

- tui/latex.go + latex_parser.go: full latex.ts port —
  - Tables: 230-entry SYMBOLS, named/limit/display-limit/relation command
    sets, negated symbols, blackboard, super/subscript rune maps (incl.
    missing-subscript-letter gaps), spacing/negative-spacing/font-switch/
    ignored/size/plain-wrapper sets, 19 accents.
  - Script formatting: normalizeScriptValue (spacing collapse around
    =+-), unicode super/sub with per-rune fallback, formatScript
    single-char / ASCII-letters-only sub parenthesization (Greek falls to
    parenthesized — TS /^[A-Za-z]+$/), formatFraction simple-numerator/
    denominator heuristics, formatRoot letter-number check.
  - Layout engine: private-use-plane markers (single runes: \U000f0000-5),
    named-operator spacing via MANUAL scan (Go regexp has no lookbehind),
    space collapse + blank-edge-line filter, pad/join/renderLayout for
    stacked fractions (centered bar), operator limits (upper/lower), script
    stacks, matrices; trailing-marker '.' append to matrix last line;
    preserve-space rules around matrix nodes; min-indentation strip.
  - Parser: parseSequence (groups, escapes incl. newline-after-backslash
    control space, negative-space trimEnd + named-operator-end reorder,
    scripts w/ named-operator-end reorder, whitespace collapse, =/</>
    spacing, & skip, ~ space), parseScripts (paired sub+sup, unicode-vs-
    layout decision: display && no-slash && (nested depth | missing unicode
    table entry)), parseCommand (full dispatch: symbols w/ cdot/times/
    relation spacing, named operators w/ markers, \not negation incl.
    combining-slash fallback, limit operators w/ \limits/\nolimits modifier
    scan (lookahead replaced by manual letter check), \frac/\dfrac/\tfrac
    display stacking w/ stackFractions threading (scripts pass FALSE),
    \sqrt w/ optional degree 2/3/4 + superscript degree, \boxed/\fbox,
    \binom, accents (single char attaches, else name(...)), \mathbb,
    \operatorname*, \mod/\pmod, \overset/\underset/\stackrel, plain
    wrappers, \begin environments), parseOperator (script loop w/ duplicate
    detection, display limit layout vs inline bracket/script styles),
    required/optional arguments, readRawGroup (escape-aware depth walk),
    row split on \\\\[dim], environments: equation/displaymath, align family
    (alignedat pair-merging), cases (natural-condition prefixes, value
    alignment w/ protected space, odd/even brace centering), matrix family
    (column width alignment, per-env delimiters), renderNested (display &&
    stackFractions).
  - GO TRAPS FIXED (3): regexp lacks negative lookahead (manual check) and
    lookbehind (manual scan); parser slice helper returned source-to-end
    instead of [start,position) — broke command names AND readRawGroup
    (2 sites); normalizeOutput right-spacing built into a Builder whose
    output was never read back.
  - Script semantics pinned: subscripts fall back to parentheses for
    multi-char non-ASCII (μλ → _(μλ)), script arguments never stack
    fractions, ASCII-letters-only implicit subscript.
- tui/latex_test.go: FULL latex.test.ts port — 11 groups, ~110 exact-output
  cases: 4 session corpora (Jacobian conjecture, satellite, matrix,
  extended formulas incl. multi-line matrices w/ 40-space indentation),
  symbols/roots/sums/accents/binomials/negations/joins/delimiters/
  operatorname/mod/overset/sqrt-degrees/font-wrappers/environments, cases
  brace centering (odd + even), display matrices w/ stacked π/4 fraction
  adjacency, display fractions + nested-linear, script layouts incl. the
  gnarly ∂ₜU₂/x^{n²}/x_{i_j} composition, operator limits (\nolimits
  inline, \limits stacked), spacing normalization (4 newline variants,
  named-operator adjacency), backslash-CRLF control space, unsupported +
  malformed rejection (5 cases). ALL GREEN.

## CA1 ROUND-12 DONE (tui markdown lexer: marked-compatible token trees)

- DEPENDENCY DECISION: markdown.ts embeds the `marked` v18 parser (npm). The
  lexer subset the renderer exercises is ported by hand (tui/
  markdown_tokens.go + markdown_block.go + markdown_inline.go) with Node as
  the ORACLE: /tmp/md-golden/dump-tokens.mjs reconstructs the reference's
  exact parser configuration (StrictStrikethroughTokenizer + latex
  block/inline extensions) and dumps token trees as JSON; tui/
  markdown_lexer_test.go compares the Go tree against the dump (raw fields
  stripped as implementation detail).
- Token model: 15 sealed types — block (heading/paragraph{Text=source
  text}/code/list{ordered,start,loose,items{task,checked,checkbox-token,
  text}}/table{header,align,rows}/blockquote/hr/space/html/text-block/
  latexBlock{pending}) + inline (text/strong/em/codespan/link{href,text}/
  del/escape/br/latex{pending}/checkbox).
- Block lexer: tab->3-space normalization, space tokens between blocks,
  ATX headings (trailing #'s trimmed), fenced code (``` / ~~~ with
  fence-width content dedent, unterminated to EOF), HR, blockquote (lazy
  continuation), lists (ordered/unordered markers, nesting by content
  column, loose detection via blank+continuation lookahead, lazy paragraph
  lines, task markers -> leading checkbox token, TIGHT items hold text
  blocks while LOOSE hold paragraphs — marked distinction), tables (pipe
  split with escaped pipes, alignment, ragged rows padded), paragraph
  accumulation until block starts, latex BLOCK tokens ($$..$$ and \\[..\\]
  with pending streaming forms; mid-line closings fall to the pending form
  which consumes the entire remaining input — 3 BUGS FIXED here: mid-line
  consumption loop, remainder re-emission, advance-to-end).
- Inline lexer: escapes (punctuation set), codespans (manual backtick-run
  matcher — Go RE2 has no backreferences — with marked's space-strip
  + newline-flatten), inline latex ($$/(..)/[..]/$..$ with the full
  disambiguation table: trailing-space/leading-digit/CONSTANT_like/
  backtick rejection, pending fallback with looksLikePendingDollarMath),
  autolinks <scheme:>/<email> (plain text children — re-lexing the URL
  would recurse infinitely through the bare-link matcher — FOUND BY
  DEBUGGING a stack overflow), links via a hand-rolled bracket-aware
  matcher (RE2 group semantics drift), strict strikethrough (manual
  matcher, no lookahead), strong/em with delimiter-run rules + intraword _
  restriction + escaped-char skipping, bare GFM URLs/emails at run starts
  (plain children), 2-space line breaks, text-run splitting on specials
  with adjacent merge.
- GO TRAPS (4 more): raw-string fence regex containing backticks (concat),
  RE2 rejects lookaheads/backrefs/\\1 (three manual matchers), bare-link
  recursion, pending-latex line advance. Golden harness itself: marked
  emits raw=text on paragraphs, checked only on task items, start:"" on
  unordered lists, checkbox tokens inside task items — all discovered via
  JSON-tree semantic diffing and matched.
- Golden corpus: 27 representative cases (headings x3, paragraphs, simple/
  nested/ordered/mixed lists, emphasis combo, blockquote, hr, fenced code
  x2, table x2, links, latex block/inline/pending, escapes, task lists,
  loose lists, continuation paragraphs, aligned tables, autolinks, bare
  email). 27/27 GREEN. Renderer + full markdown.test.ts port next round.

## CA1 ROUND-13 DONE (tui markdown renderer + test batch 1)

- tui/markdown.go: full markdown.ts RENDERER port —
  - Markdown component: transform seam w/ source+width caching, tab
    normalization, token dispatch, wrap-then-pad pipeline (image lines pass
    through unwrapped/escaped), bg-extension via applyBackgroundToLine,
    vertical padding, cache w/ setText/invalidate.
  - trimPartialClosingFences (streamed code fences, pi #5825): list-last-
    item + blockquote recursion, fence-marker prefix + partial-last-line
    check stripping text.
  - Default style machinery: applyDefaultStyle (fg + decorations, bg only
    at padding), style-prefix extraction via NUL sentinel through the theme
    chain, getDefaultInlineStyleContext.
  - renderToken: heading (h1 = heading(bold(underline)), h3+ prefix
    verbatim, heading-scoped inline context), paragraph (+list/space-aware
    spacing), text block, latexBlock (display render w/ raw fallback +
    pending raw), code (fence borders, highlightCode seam, 2-space indent),
    list dispatch, table dispatch, blockquote (quote(italic) prefix re-
    applied after every inline reset, width-2 border, block children via
    renderToken with identity default style, trailing blanks popped), hr
    (min(width,80)), html passthrough, space.
  - renderInlineTokens: latex (rendered w/ raw fallback), escape
    (preserve option), text w/ nested tokens, strong/em/del via theme +
    stylePrefix re-append, codespan, link (OSC 8 when hyperlinks; else
    href-in-parens with mailto: comparison), br, html, trailing stylePrefix
    trim loop.
  - renderList: 4-space nesting, ordered numbering from start, marker
    preservation (regex ordered / manual unordered w/o lookahead), task
    markers in bullet, first/continuation prefixes, per-item wrap width,
    nested-list recursion, loose blank lines.
  - renderTable: border overhead 3n+1, narrow fallback to raw, natural +
    min-word column widths (30 cap) with weighted shrink/grow + leftover
    distribution, box borders, styled bold header, per-cell wrap w/ style
    reset + prefix restore between fragments, separator between rows.
- tui/markdown_test.go — 35 tests from markdown.test.ts batch 1: transform
  caching (4-call sequence), nested lists (2/4-level/ordered/normalize/
  preserve incl. ITEM RAW FIX: the lexer now keeps the marker line in
  MdListItem.Raw for marker preservation), task lists, loose blank lines,
  heading bold + single blank + no trailing blank, code blocks (basic/lang/
  last-block), hr (width/blank/no-trailing), blockquote borders, inline
  style battery (em/strong/code/del/tilde-literal), escape normalize/
  preserve, tables (simple/dividers/alignment/wrap w/ per-row borders/last-
  block), paragraph spacing, padding, latex (inline/block/disabled/pending/
  currency), link fallbacks (email no-mailto/bare URL once/parens/mailto
  parens), OSC 8 hyperlinks (open/close sequences + no inline parens),
  escaped-quote, thinking-trace gray-italic + bold style persistence.
  GO TRAPS this round: raw-string regex containing backticks again,
  rune-vs-byte width assertions (─ is 3 bytes), padded blank lines vs
  empty strings, chalk assertion level (joined-output containment, not
  prefix adjacency).
- Markdown test suite now 35/35 GREEN; full tui suite + 2x count=2 stress
  + gates green.

## CA1 ROUND-14 DONE (tui autocomplete: combined provider + tests)

- tui/autocomplete.go: full autocomplete.ts port —
  - Prefix machinery: path delimiters (space/tab/quote/squote/=), last-
    delimiter scan, opening-wrapper strip (( [ { < ` w/ closed-inside
    retention), unclosed-quote extraction (quote-flip scan, @-anchored and
    plain, token-start validation), path-prefix parse (@" / " / @ / bare),
    completion-value building (quote when path contains separators — ANY
    rune not just the first, BUG FIXED).
  - CombinedAutocompleteProvider: @-prefix fuzzy file suggestions (fd two-
    pass: depth-1 base + full recursive, dedup, scoring exact=100/
    prefix=80/substring-name=50/substring-path=30 + dir bonus 10, sort by
    score/depth/length/path, top 20, scoped queries re-basing ~/ / abs /
    relative), slash-command completion (bare-name fuzzy first with skill:
    prefix stripping, full-name-only fallback, hint—"desc" description
    join), command-argument completions via GetArgumentCompletions, path
    completion (root/directory/file-prefix modes, home expansion w/
    trailing-slash preservation, ~//abs/relative search-dir resolution,
    display-path reconstruction preserving ~/ ./ ~ prefixes, symlink-to-
    directory stat, dirs-first-then-alpha sort), applyCompletion (slash
    command "/value ", @-attachment no-space-after-dir, command-arg
    context detection, quote-dedup on closing overlap), Tab gating
    (slash-command w/o space suppressed).
  - fd walker: exec.CommandContext w/ abort-signal cancellation, identical
    flag set (--hidden --follow .git excludes, max-results, optional depth,
    full-path when query has /, escaped-segment path query), output parse
    w/ .git filtering and trailing-slash directory marking.
  - GO/SEMANTICS FIXES (3): JS \\\\s ≠ RE2 \\\\s (U+00A0/U+3000/etc. — explicit
    jsWhitespaceClass), CJK punctuation in Common script (。、《》「」— Go
    script tables miss them, supplementary RangeTable + token-start class),
    whole-path separator scan for quoting.
- Tests: 15 ported covering the non-fd suite (fd @-batch needs fd binary;
    runs live when present): forced path extraction (4), CJK prefixes after
    12 separators x 4 completions with apply, separator-vs-literal-path
    disambiguation incl. quoted round-trip + missing rejection, empty
    prefix after 5 separators force/natural, opening wrappers (7 wrappers x
    2 prefixes), quoted-after-wrapper, closed-wrapper retention, ./ prefix
    preservation (file + dir), quoted direct completion round-trip across 4
    separators, dirs-before-files, quoted continuation, quote-dedup apply,
    slash command + argument completions, Tab gating. 15/15 GREEN.

## CA1 ROUND-15 DONE (tui editor: the 2472-line editor + test batch 1)

- tui/editor.go + editor_input.go: full editor.ts port —
  - Paste-marker machinery: marker regexes, isPasteMarker, segmentWith
    Markers (atomic merge for valid IDs only, byte-indexed segments over
    grapheme/word modes), WordWrapLine (wrap-opportunity backtracking w/
    width accounting, force-break fallback, atomic-segment wider-than-max
    visual re-wrap, whitespace + CJK break opportunities) — VERIFIED
    against the reference by re-implementing the TS loop in Node ("hello
    world"@5 = hello/" "/world).
  - Editor: state/history/kill/paste registries (pasteOrder list for
    deterministic iteration), focused flag, render geometry tracking
    (lastWidth, visible count, autocomplete height), scroll offset w/
    30%-of-rows clamping, scroll borders (createScrollBorder label/-
    indicator/ellipsis fallbacks), layoutText (chunk cursor ranges incl.
    trimmed-whitespace clamp), render (padding-aware cursor rendering
    w/ CURSOR_MARKER + inverse-video grapheme, cursor-in-padding flag,
    autocomplete list below), HandleInput full dispatch (jump mode w/
    printable decode + re-trigger cancel, bracketed paste incl. CSI-u
    ctrl decode, autocomplete-mode navigation/tab/confirm w/ slash-
    command fallthrough-to-submit, tab completion trigger, all deletion
    bindings + shift+backspace/delete, kill ring, dedicated history
    keys, cursor movement w/ first/last-visual-line history gating,
    new-line detection incl. legacy \x1b\r and csi-u shift-enter,
    backslash+Enter workaround both directions, page scroll, jump
    triggers, shift+space, printable decode then >= 32 filter).
  - Cursor engine: buildVisualLineMap, findVisualLineAt/Current,
    moveToVisualLine (sticky column decision table w/ preferred col,
    snapped-from resolution, continuation-VL skip for atomic segments,
    snap-to-segment-start), moveCursor grapheme-stepping + end-of-buffer
    preferred col, pageScroll.
  - Editing: insertCharacter (undo coalescing, autocomplete auto-trigger
    on /@# trigger chars + word chars + CJK in slash/symbol contexts),
    handlePaste (marker insertion > 10 lines / > 1000 chars, CSI-u
    decode, non-printable filter, path-space heuristic), addNewLine,
    submitValue (marker expansion + registry clear), backspace w/ paste
    marker deletion + registry shift-down + renumber, forward delete,
    delete to line start/end + word variants w/ newline kills, yank/
    yankPop (insertYankedText/deleteYankedText multi-line), undo
    snapshots (state + paste registry), jumpToChar multi-line search,
    word navigation w/ atomic segment option.
  - Autocomplete lifecycle: provider install + trigger characters
    merge, best-match selection (exact > prefix), list creation w/ slash
    layout, request pipeline (force gating via ShouldTriggerFile
    Completion, debounce for attachment contexts, abort controller,
    start-token/request-id/text snapshot currency checks), single-item
    explicit-Tab auto-apply, cancel/update paths.
- GO TRAPS (4): trigger-pattern regexes with wrapper runs — character
  class [([{<` broke RE2 parsing (restructured as explicit groups);
  \\s double-escaping in interpreted strings; SelectList mouse returns
  plain results (wrapped via DispatchMouseEvent); abort controller
  direct wiring.
- tui/editor_test.go batch 1 — 21 tests: history battery (empty, most
  recent, cycles w/ clamp at oldest, jump-to-start-then-history from
  draft + draft restore, down navigation + exit, exit-on-typing ["xold
  prompt" — REFERENCE expectation], exit-on-setText, consecutive dedup,
  non-consecutive duplicates, cursor-over-content, 100-entry limit,
  cursor placement up/down multi-entry + opposite-direction movement),
  accessors (cursor position, defensive lines copy), backslash+Enter
  (immediate insert, newline conversion, submit-after-chars), Kitty
  CSI-u (press/shifted/modifyOtherKeys), unicode editing (combining
  backspace, astral cursor), scroll indicators render, wordWrapLine
  (word-boundary chunks w/ space chunk, force-break, CJK). 21/21 GREEN.
- Full suite green; 2x count=2 stress; gates 7/7; 3-platform build.

## CA1 ROUND-16 DONE (tui editor tests batch 2: kill ring + undo)

- tui/editor_kill_undo_test.go — 22 more editor tests from the reference
  suite:
  - Kill ring battery: W/Y round-trip, U/K saves, empty-ring no-op, yank-pop
    cycling w/ wrap, guards (not-after-yank, single-entry), consecutive W
    accumulation, multi-line U accumulation (5-step "1\n2\n3" deletion
    coalescing into one entry incl. newlines), forward/backward append/
    prepend during accumulation, non-delete breakers (typing splits entries,
    yank-most-recent then pop-to-older), non-yank chain break, rotation
    persistence, cross-line coalesce (reference "1\n2\n3" full-delete
    scenario), Ctrl+K at line end (newline deletion + continuation), Alt+D
    forward word delete + newline merge, yank/yank-pop in middle of text,
    multi-line yank and pop.
  - Undo battery: empty stack on fresh editor, word-coalescing (space
    captures before it — one undo removes " world"), spaces one-at-a-time
    (via reference two-space chain), backspace, forward delete, Ctrl+W,
    newline, bracketed paste, submit-clears-stack.
  - TEST-SPEC FIXES (from reference verification): TS undo test expects
    "hello" after ONE undo of "hello world" (space-before semantics — my
    earlier three-step expectation was wrong), multi-line Ctrl+W only
    deletes the last word (pop yields "l3xx" not the full multi-line),
    empty-stack test uses a fresh editor (SetText pushes undo), cross-line
    coalesce follows the exact reference 1\n2\n3 scenario (my synthetic
    "hello world\nsecond line" cursor assumptions were wrong).
- Editor test total: 43/43 GREEN (batch 1 + batch 2). Full tui suite +
  2x count=2 stress + gates 7/7 + 3-platform build all green.

## CA1 COMPLETE — marked done; CA2 STARTED

## CA2 ROUND-1 DONE (codingagent foundation: config + core utils)

- **CA1 CLOSED**: all 43 tui TS sources mapped (39 ported 1:1, 3 native NAPI
  modules documented deviation, index.ts barrel inlined); 261 tests green;
  tui/ = 70 files ~37.7K Go lines.
- codingagent/config.go: full config.ts port — runtime detection collapsed
  to constants (Go binary replaces Bun/Node; documented), InstallMethod
  detection (exec-path heuristics incl. pnpm/yarn/bun/npm prefix matching),
  SelfUpdateCommand chain building (display quoting, uninstall+install
  steps), readCommandOutput (sync exec w/ requireSuccess error path),
  package-asset paths (FindNodePackageDir w/ dist/ deferral, PI_PACKAGE_DIR
  override, themes/export-html/interactive-assets src-or-dist resolution),
  app config from package.json (piConfig name/configDir -> AppName/AppTitle/
  ConfigDirName/Version w/ sync.Once lazy load, ENV var names derived from
  app name), share viewer URL, user config paths (agent dir w/ env override,
  themes/models/auth/settings/tools/bin/prompts/sessions/debug-log).
- codingagent/paths_text.go: utils/paths.ts (NormalizePath w/ tilde/trim/@/
  unicode-spaces/windows-shell-path options, file:// URL decode, IsLocalPath
  protocol prefixes, ResolvePath, GetCwdRelativePath inside-cwd check,
  FormatPathRelativeToCwdOrAbsolute, CanonicalizePath symlink fallback,
  FileRevision — Go os.FileInfo lacks dev/ino; size+mtime+mode carries the
  change-detection contract, documented equivalence), utils/text.ts (BOM
  split/strip), utils/json.ts (JSONC comment + trailing-comma stripping
  w/ string-literal preservation), utils/pi-user-agent.ts (go/ runtime
  segment).
- codingagent/utils_core.go: utils/frontmatter.ts (YAML frontmatter w/
  CRLF normalization + unterminated passthrough), utils/deprecation.ts
  (once-per-message warning), utils/sleep.ts (abort-aware), utils/abort.ts
  (OperationSignal, RaceWithAbortSignal w/ abandoned-operation settle),
  utils/mime.ts (image sniffing: JPEG w/ 0xf7 XR rejection, PNG w/ IHDR +
  acTL/IDAT animation walk, GIF, WEBP, BMP w/ DIB header family +
  compression checks).
- Tests (16, ported from config.test.ts + paths + text + json + mime
  surfaces): BOM, JSONC battery, NormalizePath options, unicode spaces,
  IsLocalPath protocol table, ResolvePath, cwd-relative, frontmatter (parse
  + strip + CRLF + unterminated), deprecation, image MIME battery, animated
  PNG chunks, BMP DIB, user agent, self-update step/chain, env override.
  16/16 GREEN. GO TRAP: regexp \u escapes rejected in raw strings —
  interpreted strings required.

## CA2 ROUND-2 DONE (codingagent shell + truncate + path-utils + NFD)

- codingagent/shell.go (+shell_unix.go/shell_windows.go): utils/shell.ts —
  ShellConfig w/ stdin-vs-argv transport, legacy-WSL bash detection, shell
  resolution chain (custom path -> Windows Git Bash locations -> PATH ->
  /bin/bash -> sh, with the reference's error text), PowerShell config
  (pwsh preferred), GetShellEnv (managed bin dir prepended to PATH, env-key
  case-insensitive replacement), SanitizeBinaryOutput (control/format-char
  filter preserving \t\n\r), detached-child tracking (mutex-guarded set),
  KillProcessTree (process-group kill on Unix / System32 taskkill on
  Windows, split into build-tagged files — syscall.Kill is Unix-only).
- codingagent/truncate.go: core/tools/truncate.ts — TruncateHead (never
  partial lines; first-line-exceeds-limit sentinel), TruncateTail (partial
  first line via UTF-8-boundary-aligned end slice), TruncateLine (rune-
  counted clip + "... [truncated]"), FormatSize.
- codingagent/path_utils.go: core/tools/path-utils.ts — read-path
  resolution with macOS filename variants: AM/PM narrow no-break space,
  NFD, curly apostrophe, and combined NFD+curly (French screenshots);
  ExpandPath/ResolveToCwd shared options.
- codingagent/nfd_data.go + ccc_data.go: GENERATED Unicode tables (13,253
  canonical decompositions via Node ICU oracle; 399 combining-class ranges
  from UCD 16.0.0 DerivedCombiningClass.txt, SORTED by code point — the UCD
  file groups by class, which broke the first binary search) +
  NormalizeNFD (full decomposition + canonical-ordering insertion sort;
  KEY-INVARIANT FIX: the sort key must stay fixed at its starting index,
  not recomputed after swaps). Golden: testdata/nfd-golden.json (35
  codepoint + 6 sequence samples incl. Hangul, Tibetan, reordering cases)
  generated from Node's normalize("NFD").
  Dep note: hand-rolled NFD instead of golang.org/x/text keeps the
  only-yaml external dependency convention.
- Tests (+10, total 30): truncate head/tail/line/format, sanitize, shell
  config resolution + custom path + legacy WSL + shell env, expand/resolve,
  NFD golden + identity + variant resolvers (curly-quote on-disk variant
  verified; the NFD-on-disk case is platform-flaky — APFS is normalization-
  insensitive — so NFD is pinned by the golden instead).

## CA3 ROUND-1 DONE (tools foundation: definition wrapper, mutation queue, write, read, image pipeline)

- codingagent/tool_definition.go: tool-definition-wrapper.ts + the
  ToolDefinition surface of extensions/types.ts (full interface lands with
  the extensions phase) — ToolDefinition (prompt snippet/guidelines, TypeBox
  params, constrained sampling, render shell, prepare shim, execution
  mode), WrapToolDefinition bridging into the ported agent.AgentTool (with
  toAgentToolResult mapping), WrapToolDefinitions,
  CreateToolDefinitionFromAgentTool (string->enum mode conversion).
- WithFileMutationQueue: file-mutation-queue.ts — RACE FIX over the
  reference-faithful first draft: naive delete-on-release lets a late
  registrant bypass a draining chain (Go goroutines expose what the TS
  event loop hides); entries now carry waiter counts and delete only when
  unwitnessed. DOCUMENTED REFERENCE BEHAVIOR the tests must respect: a
  missing file keys the queue by path while an existing one keys by real
  path (macOS /tmp -> /private/tmp), legitimately splitting queues across
  the first creation.
- codingagent/write_tool.go: write.ts — pluggable WriteOperations (local
  default), prompt contribution, abort observed between steps (queue held
  until settle), mutation-queue wrap, "Successfully wrote to <path>".
- codingagent/read_tool.go: read.ts — pluggable ReadOperations, image
  branch (mime sniff -> ProcessImage -> hints + non-vision note), text
  branch (offset 1-indexed w/ beyond-EOF error, user limit, truncateHead
  with first-line-too-big sed fallback notice, lines/bytes continuation
  notices, user-limit remaining notice).
- codingagent/image_process.go + image_resample.go: image-process.ts +
  image-resize-core.ts + image-convert.ts with the photon deviation
  (Rust/WASM -> Go image stack): supported-mime normalization, PNG
  conversion, resize strategy (dimension fit -> PNG+JPEG quality ladder ->
  0.75 dimension shrink to 1x1 -> nil), base64 size accounting, EXIF
  orientation (tag parser for JPEG APP1 + 8 orientation transforms),
  Catmull-Rom resample as the Lanczos3 stand-in, ProcessImage hint chain.
- Tests (+20, total 50): mutation queue serialization (8 workers x 20
  read-modify-writes, no duplicate observations) + different-file
  parallelism, write tool (create/overwrite/custom ops/abort-without-
  create), read tool (text, offset+limit notices, line truncation, first-
  line overflow fallback, missing-file error, image attachment base64),
  ProcessImage passthrough/resize/impossible limits, ConvertToPng (jpeg ->
  png re-decode check), EXIF parse + orientation dims, wrapper round-trip,
  input mapping. Race detector clean; -count=3 stress green.

## CA3 ROUND-2 DONE (edit tool + edit-diff + NFKC)

- codingagent/edit_diff.go: edit-diff.ts — DetectLineEnding (first-
  occurrence CR/LF precedence), NormalizeToLF/RestoreLineEndings,
  NormalizeForFuzzyMatch (NFKC + per-line trailing-whitespace strip + smart
  quotes/dashes/space classes), FuzzyFindText (exact first, then normalized
  space with normalized-space offsets), ApplyEditsToNormalizedContent
  (per-edit find + occurrence-uniqueness + overlap detection, fuzzy-space
  application preserving original bytes on untouched lines via line-span
  grouping), GenerateUnifiedPatch (jsdiff FILE_HEADERS_ONLY, 4 context),
  GenerateDiffString (line-numbered display diff with leading/trailing
  context elision "...", first-changed-line capture), ComputeEditsDiff/
  ComputeEditDiff previews.
- codingagent/nfkc_data.go: GENERATED NFKC tables (4,965 compatibility
  decompositions + 1,092 composition pairs each VERIFIED via Node
  normalize) + NormalizeNFKC (decomposition -> canonical ordering ->
  composition with blocked-mark tracking). TWO ALGORITHM FIXES during
  golden testing: starters must also attempt composition (Hangul L+V is
  starter+starter), and Hangul LV+T is algorithmic (the 2-rune pair
  generator misses 3-rune Jamo decompositions — standard arithmetic
  composition added). Golden: testdata/nfkc-golden.json, 1,533 samples
  (compat-heavy BMP ranges exhaustively where NFKC differs, Hangul,
  compositions, blocked marks) — 1533/1533.
- codingagent/edit_tool.go: edit.ts — schema (path + edits[] with
  oldText/newText), prompt contribution, PrepareEditArguments (JSON-string
  edits, single-edit object, legacy top-level oldText/newText folding with
  passthrough), pluggable EditOperations, execution under the mutation
  queue (access with ENOENT/EACCES code rendering, BOM strip + restore,
  line-ending preservation, abort between steps), EditToolDetails (diff +
  patch + first changed line).
- Tests (+20, total 70): prepare-arguments battery (legacy fold, append,
  passthrough incl. non-objects, stringified edits valid + invalid JSON,
  single-edit wrap), legacy execute, basic/multi edit, error battery
  (not-found, duplicate occurrences, empty oldText, overlap, no-change,
  empty edits, missing file), fuzzy behaviors PINNED TO REFERENCE
  SEMANTICS (exact substring match wins over fuzzy — trailing spaces stay;
  smart quotes force fuzzy and the touched line is rewritten normalized
  while untouched lines keep original bytes), CRLF preservation, BOM
  preservation, detect line ending, normalize battery, fuzzy find, unified
  patch headers/body, diff elision + order-agnostic +/- check, preview
  without mutation, custom ops, abort. Race detector clean; 3x stress.

## CA3 ROUND-3 DONE (ls + grep + find + tools-manager)

- codingagent/tools_manager.go: utils/tools-manager.ts — managed-tool
  registry (fd sharkdp/fd w/ fdfind fallback; rg BurntSushi/ripgrep),
  commandExists probing, GetToolPath (managed bin dir -> system PATH),
  GetLatestVersion via the GitHub web redirect (no API quota, mirror of
  pi's quota-exhaustion fix), EnsureTool (offline/PI_OFFLINE + Termux
  guards w/ reference messages), downloadTool (v-prefix tags for fd,
  native archive extraction: tar.gz + zip via stdlib — replaces the
  reference's tar/unzip/powershell process spawning, trailing cleanup),
  path-traversal-guarded extraction, recursive binary discovery.
- codingagent/ls_tool.go: ls.ts — pluggable LsOperations, case-insensitive
  alpha sort, "/" suffix via per-entry stat w/ skip-on-error, entry limit
  (500 default) + byte truncation with combined notices
  ("N entries limit reached. Use limit=2N for more" + "50.0KB limit
  reached"), "(empty directory)".
- codingagent/grep_tool.go: grep.ts — ripgrep JSON-event streaming (--json
  --line-number --hidden + ignoreCase/fixed-strings/glob), match-limit
  kill of the child (limit exit tolerated), context blocks via cached
  per-file reads with ":" match / "-" context separators, line truncation
  at 500 chars w/ notice, byte truncation, custom ops for isDirectory/
  readFile, single-file basename formatting vs directory-relative posix
  paths.
- codingagent/find_tool.go: find.ts — RelativizeFindResultPath (posix
  separators + trailing-slash preservation), custom-Glob delegation with
  the short limit notice (no refine hint), fd execution (--glob --hidden,
  git-repo walk for --no-require-git per pi#5960, --full-path for patterns
  containing "/" with **/ prefixing, Windows separator classes), result
  limiting + notices.
- Tests (+18, total 88): ls (basic w/ directory suffix + dotfiles,
  case-insensitive sort, empty, limit notice + details, path-not-found /
  not-a-directory, explicit path), tools-manager (PATH/managed resolution,
  unknown tool), grep (basic multi-match, no-matches, ignoreCase/glob/
  literal, context separators, limit kill, missing path, single-file
  basename, abort), find (relativize table, basic + deep patterns [SKIP
  when fd absent, matching the reference's skip], no matches, limit,
  custom glob incl. notice-shape difference, missing path). Race detector
  clean; 3x stress; PI_OFFLINE env honored.

## CA3 ROUND-4 DONE (bash tool + output accumulator) — tools complete

- codingagent/output_accumulator.go: output-accumulator.ts — bounded-memory
  streaming: chunked UTF-8 decoding with pending partial sequences, decoded
  rolling tail (2x maxBytes) with boundary-aligned trimming + line-boundary
  tracking, line/byte counters, spill-to-temp-file when limits exceeded
  (raw chunks replayed into the file at open), Snapshot with tail
  truncation + forced persistence, LastLineBytes. BUG: Go's zero value
  left tailStartsAtLineBoundary false (TS inits true) — snapshots dropped
  the first line; fixed via constructor init.
- codingagent/bash_tool.go (+detached_unix/windows.go): bash.ts — the
  shared shell-tool factory (bash + powershell configs) with timeout
  validation (max 2^31-1 ms), local shell operations (detached process
  groups on Unix, stdin-vs-argv command transport, streamed stdout+stderr,
  abort/timeout process-tree kills with untracking, 128+signal exit-code
  convention for signal-killed shells, missing-cwd error), resolveSpawn
  Context (PI_* session env scrub/restore; the full ExtensionContext model
  surface lands with extensions), throttled onUpdate streaming (100ms
  coalescing, immediate first update), OutputAccumulator integration with
  truncation notices ("Showing last X of line N (line is Y)" /
  lines-range / bytes-limit variants + Full output path), appendStatus
  error composition for abort/timeout/nonzero-exit/no-exit-code.
  RACE FIX found by -race: timedOut written by the timer goroutine and
  read after Wait — atomic.Bool.
- Tests (+24, total 112): accumulator (basic verbatim snapshot incl.
  trailing newline, split-rune streaming, truncation spill incl. exact
  byte count of the spilled file, rolling-tail bound, finish-then-append
  panic, last-line bytes), bash (basic, exit-code error with stderr,
  no-output, 1s timeout kill, abort kill, invalid timeout, extension-cwd
  pwd [symlink-resolved comparison for macOS /var -> /private/var],
  missing cwd, command prefix env, truncation spill notice + file,
  spawn hook, session-env hidden, stdin+argv transports, custom ops,
  surviving-child tree kill, streaming updates), spawn-context env
  resolution, timeout validation table. Race detector clean; 3x stress.

## CA4/CA5 ROUND-1 DONE (extension foundations: event bus, timings, source info, manifest, exec, messages)

- codingagent/event_bus.go: event-bus.ts (mutex-guarded per-channel handler
  lists; handler panics reported not propagated — the async-safeHandler
  equivalent), timings.ts (PI_TIMING=1 gated namespaces + stderr reports),
  source-info.ts (SourceScope/Origin, PathMetadata, synthetic defaults),
  pi-manifest.ts (package.json pi field -> extensions/skills/prompts/themes
  with per-field string validation).
- codingagent/exec.go: exec.ts — ExecCommand with timeout + abort kills
  (context composition), nonzero exit propagation, spawn failures resolving
  code 1 (never rejecting, mirroring the reference promise).
- codingagent/messages.go: messages.ts — the four custom AgentMessage roles
  (bashExecution w/ excludeFromContext, custom extension messages,
  branchSummary, compactionSummary) on the ported agent.AgentMessage
  interface, CompactionSummary/BranchSummary prefix/suffix constants,
  BashExecutionToText (fenced output, cancelled/exit-code/truncation
  notices), ISO-or-millis timestamp parsing, ConvertToLlm (context-excluded
  drops, custom string->text content, prefix framing, standard-role
  passthrough) against the ported ai.Content constructors.
- Tests (+11, total 123): event bus (emit/subscribe/unsubscribe, multi
  subscriber, panic containment, clear), source info, pi manifest (valid/
  no-pi/mixed-types/broken/missing), exec (echo, exit code, timeout kill,
  abort kill, missing command), bash execution text variants, message
  constructors, ConvertToLlm full battery (exclusion, string content,
  prefixes, passthrough), timings no-op smoke. Race detector clean.

## CA5 ROUND-1 DONE (extension foundations + session-manager core)

- codingagent/event_bus.go: event-bus.ts + timings.ts + source-info.ts +
  pi-manifest.ts — mutex-guarded pub/sub with panic containment, PI_TIMING
  profiling namespaces, SourceScope/Origin attribution, package.json pi
  manifests with per-field string validation.
- codingagent/exec.go: exec.ts — abort+timeout-aware command execution
  resolving (never rejecting) with captured stdout/stderr/code.
- codingagent/messages.go: messages.ts — the four custom AgentMessage roles
  on the ported agent interface, CompactionSummary/BranchSummary framing,
  BashExecutionToText, ConvertToLlm against ai.Content constructors.
- codingagent/session_entries.go + session_files.go + session_manager.go:
  session-manager.ts (~2008 lines) — the append-only session tree:
  - Entry model: 11 entry types + header as FileEntry interface w/
    entryType()/base() discriminants, JSONL parse with malformed-line skip.
  - Migrations: v1->v2 (id/parentId linkage + firstKeptEntryIndex ->
    firstKeptEntryId), v2->v3 (hookMessage -> custom), idempotent.
  - Persistence: LoadEntriesFromFile (final-newline repair, header
    validation), ReadSessionHeader bounded scan w/ scan-limit error,
    lazy file creation (setup-only sessions leave no file; first user
    message creates it — #10000), append-only writes after flush,
    rewrite-on-migration.
  - Tree: leaf-based GetBranch/GetTree (label-aware, timestamp-sorted
    children), Branch/ResetLeaf/BranchWithSummary, GetChildren, labels
    with set/clear semantics.
  - Appends: message/thinking-level/model-change/usage/compaction/custom/
    custom-message/session-info/context-edit (branch-local with editable-
    role validation and assistant/toolResult string->blocks normalization).
  - Projection: BuildSessionPath (leaf-to-root), BuildContextEntries
    (newest compaction stands in for summarized prefix, system messages
    filtered post-compaction), BuildSessionProjection (context edits apply
    replacement-or-omission; older compactions inside the retained range
    contribute nothing), session-context settings resolution
    (thinking-level/model incl. assistant-message model attribution).
  - Discovery: FindMostRecentSession (mtime sort + header cwd match),
    BuildSessionInfo streaming summary (first user message, all-messages
    text, activity-time modified), ListSessionsFromDir with bounded
    concurrency + progress publishing.
- Tests (+21, total 144): event bus battery, manifest battery, exec
  battery, message text/constructors/ConvertToLlm, session append+branch,
  tree, labels, lazy persistence + reload, projection w/ compaction
  retention, context settings, context edits (apply/null-omit/non-editable
  error), reset leaf, branch-with-summary, JSONL parse, both migrations,
  id validation table, path building, FindMostRecentSession (cwd filter +
  newest-overall), BuildSessionInfo, ListSessionsFromDir, invalid-file
  rejection. Race detector clean.

## CA5 ROUND-2 DONE (session-manager statics + minimatch + model-resolver)

- codingagent/session_manager.go (continued): CreateBranchedSession (label
  entries stripped from the path and re-chained past the retained entries,
  compaction firstKeptEntryId remapping across dropped labels, new header
  with parent linkage, same lazy-write rule as persistEntry) + the static
  factories: SessionManagerCreate/Open (bounded-header fallback to full
  load on scan limit)/ContinueRecent (cwd-filtered newest)/InMemory/
  ForkFrom (header rewrite + entry copy into the target project dir)/
  FindByID / List (cwd filter via custom-dir detection) / ListAll (all
  project dirs, mtime+name sorted).
- minimatch/ (new package): npm minimatch v10.2.6 subset — basic
  wildcards (*, ?, [...] incl. ! negation), globstar (**), segment-scoped
  matching, nocase; extglob and brace expansion deferred to their first
  in-repo consumer (documented in the package comment).
- codingagent/model_resolver.go: model-resolver.ts resolution half —
  DefaultModelPerProvider (all 44 providers), isAlias (-latest / no
  -YYYYMMDD), FindExactModelReferenceMatch (canonical provider/id, split
  provider+id, bare id; ambiguity rejected), tryMatchModel (partial id/
  name with alias-then-dated preference, descending sort), ParseModelPattern
  (recursive colon splitting for thinking levels with strict-vs-warn
  fallback modes), buildFallbackModel (custom ids on known providers),
  ResolveModelScopeFromModels (exact + glob patterns via minimatch nocase
  matching on provider/id and bare id, ":level" suffix extraction, dedup
  via ModelsAreEqual, no-match/invalid-thinking-level diagnostics).
  Remaining for next round: resolveCliModel + findInitialModel +
  restoreModelFromSession (need ModelRuntime).
- Tests (+10 resolver, +5 session, +3 minimatch; total 159): exact/partial
  matching battery, alias preference, thinking-level split battery (valid/
  invalid/strict), glob scoping (provider/content/level/no-match), dedup,
  fallback construction, alias table, branched-session file round-trip
  (messages + re-chained label, reload), create/open/continue/findById,
  in-memory + fork (incl. missing-source error), list/listAll across
  projects, default session dir encoding, minimatch battery (wildcards/
  segments/globstar/classes/nocase/regex metachar quoting). Race clean.

## CA5 ROUND-3 DONE (config-value resolution + models.json snapshot + typebox fix)

- codingagent/resolve_config_value.go: resolve-config-value.ts — template
  parser ($VAR / ${VAR} with $$ and $! escapes, invalid/unterminated
  braces stay literal, adjacent vars, literal coalescing), command
  execution ("!..." prefix, 10s timeout, process-lifetime cache with
  nil-vs-set distinction, uncached bypass), env resolution (explicit map
  over process env), GetConfigValueEnvVarName(s)/Missing/IsConfigured,
  ResolveConfigValueOrThrow's three failure shapes (command / single var /
  multi var), ResolveHeaders(OrThrow).
- codingagent/model_config.go: model-config.ts — the full models.json
  schema (models, overrides, provider entries with apiKey/api/oauth/
  headers/compat/authHeader/modelOverrides) as TypeBox schema compiled via
  the ported typebox.Validator, JSONC+BOM tolerant parse, ModelsJson*
  typed structs, immutable ModelConfig snapshot (GetProvider/IDs/Error)
  with parse/schema/load error messages matching the reference.
- **TYPEBOX FIX** (upstream gap found by the model-config tests): builder
  options (NumberOption("minLength", ...) etc.) only landed in Extra for
  serialization — the constraint fields used by validation were never
  hydrated, so minLength-constrained schemas validated anything.
  applyExtras now routes through a shared hydrateConstraints (same table
  as the JSON decode path); typebox suite still green repo-wide.
- Tests (+8, total 167): literals/escapes battery, env interpolation
  (bare/braced/embedded/adjacent/missing/process-fallback), commands
  (detect/execute/fail/cache/uncached), env-name introspection, or-throw
  failure shapes, headers (resolve-drop/throw), models.json load battery
  (valid JSONC + overrides, missing file, empty path, broken JSON, schema
  violations incl. minLength, missing providers key). Race clean.

## CA5 ROUND-4 DONE (provider-composer)

- codingagent/provider_composer.go: provider-composer.ts model layers —
  applyModelsJson (baseUrl rewrite incl. radius-oauth passthrough, custom
  model upsert by id with findModelDefaults fallback chain [exact id ->
  provider api -> openai-completions -> first], empty-config rejection,
  oauth-without-baseUrl rejection, contextWindow/maxTokens validation),
  applyExtension (baseUrl rewrite without models; full model replacement
  with the same defaults chain; per-type api resolution),
  applyModelOverride (name/reasoning/contextWindow/maxTokens/input/headers),
  mergeCompat (recursive nested map merge via JSON decode), rawModelHeaders
  (override + definition + extension layers matched by type and id),
  ResolveCompatibilityRequestConfig (configured headers from config AND
  extension + model-level layers, authHeader flag resolution),
  ResolveConfiguredModelHeaders, ConfiguredRequestAuthStatus (literal /
  command / env-configured / env-missing / fallback sources),
  ValidateExtensionProvider (streamSimple-requires-api eager check),
  ComposeModelProvider -> ComposedProvider (ai.Provider adapter; eager
  structural validation; auth surface: config/extension key via
  ResolveConfigValueOrThrow over inherited base resolve, OAuth
  inheritance/extension override, "no authentication method" rejection;
  name resolution precedence extension > config > base > oauth > id).
  DEVIATION (documented in the file comment): extension OAuth login/
  refresh closure machinery degrades to the ambient API-key path; the
  composed model/auth surface is preserved.
- Tests (+11, total 178): base passthrough, empty-base auth rejection +
  static-key composition w/ resolve, models.json layer (name/baseUrl/
  rename override/defaults), validation battery (empty config, oauth-
  without-url, no-api w/o defaults, no-url w/o defaults, bad window),
  extension layer (baseUrl rewrite + model replacement), streamSimple
  validation, compatibility headers (provider+extension+model-level),
  auth-status battery (nil/literal/command/env-configured/env-missing/
  fallback), mergeCompat nested merge, OAuth inheritance + replacement,
  env-key resolution through the composed auth. Race clean.

## CA5 ROUND-5 DONE (model-runtime + registry + credential stores)

- codingagent/model_runtime.go: model-runtime.ts core — ModelRuntime over
  the ported ai.CreateModels registry: builtin catalog wiring (Builtin
  Providers), models.json path resolution (explicit/absent/default
  GetModelsPath), PI_OFFLINE-gated network flag, radius provider slots
  (empty until the radius provider is ported — documented gap keeping the
  composed layer), provider ID union (builtins + native extensions +
  config + extensions), recomposeProvider (no-overlay builtins pass
  through untouched preserving exact auth/stream behavior; compose errors
  captured per-provider with base fallback), rebuildProviders, availability
  snapshot (configured/stored provider sets via auth resolve with a
  DefaultAuthContext — NIL-CTX PANIC found by tests), Register/Unregister
  Provider (eager validation), stream/complete/getAuth proxies.
- codingagent/model_registry.go: model-registry.ts — the synchronous
  extension facade over ModelRuntime (GetAll/GetAvailable/Find/
  HasConfiguredAuth/GetProviderAuthStatus/Stream/StreamSimple/Complete/
  DisplayName/Auth lookups/OAuth check/registration surface).
- codingagent/auth_storage.go: auth-storage.ts (reduced; proper-lockfile ->
  in-process mutex + 0600 file, documented deviation), models-store.ts
  (in-memory + JSON file stores with BOM-tolerant parse), runtime-
  credentials.ts (runtime API-key overlay over the credential store with
  list merging). CredentialStore adapted to the ported ai signature
  (no signal param).
- Tests (+11, total 189): builtin catalog presence, find, custom models.json
  provider (model fields/baseUrl/refresh marks configured/available),
  composition errors (PINNED REFERENCE SEMANTIC: apiKey alone is a valid
  auth-only entry — only fully-empty configs error), extension register/
  unregister, registry facade, auth storage round-trip incl. 0600 perms,
  runtime overlay (set/read/list/remove), file models store round-trip +
  second handle, concurrent modify. Race clean.

## CA5 ROUND-6 DONE (compaction pure-function core)

- codingagent/compaction_utils.go: compaction/utils.ts — FileOperations
  tracking (read/write/edit from toolCall blocks against jsonx args),
  ComputeFileLists (modified union minus read-only, sorted),
  FormatFileOperations XML tags, SerializeConversation ([User]/
  [Assistant thinking]/[Assistant]/[Assistant tool calls]/[Tool result]
  labels with 2000-char tool-result truncation marker),
  SummarizationSystemPrompt.
- codingagent/compaction.go: compaction.ts pure-function core —
  CompactionSettings + defaults (16k reserve / 20k keep-recent),
  CompactionDetails, extractFileOperations (prev-compaction merge when
  pi-generated), CalculateContextTokens (native totalTokens fallback to
  components), GetLastAssistantUsage (aborted/error/zero-usage skip),
  EstimateTokens (chars/4 across all message roles incl. system sections,
  tool-call name+args, bash command+output, summaries),
  EstimateContextTokens (last-usage + trailing estimation split),
  cut-point machinery: isCutPoint/isTurnStart predicates, FindValidCut
  Points (never tool results), FindTurnStartIndex, FindCutPoint
  (backwards accumulation to budget, nearest valid cut, adjacent-metadata
  rewind, split-turn detection), FindProjectedCutPoint (projected variant
  incl. closed-suffix omitted-assistant advancement rule).
- **SESSION-MESSAGE FIDELITY FIX**: the ported ai message structs carry
  Role() methods, not JSON fields — session round-trips lost the role tag,
  so decoded messages scored 0 tokens and matched no cut-point predicate.
  AppendMessage now injects the role via marshalAgentMessageWithRole, and
  decodeAgentMessage materializes typed shapes for estimation
  (decodedEntryMessages).
- Tests (+10, total 199): file-op extraction (incl. grep ignored, pathless
  skipped), file lists, XML formatting, serialization battery (labels,
  thinking/tool calls, truncation), token estimation per role, usage
  totals, last-assistant-usage (aborted skipped), context estimation
  (usage+trailing split), cut points (large budget keeps all / tiny budget
  cuts near end). Race clean.

## CA5 ROUND-7 DONE (branch summarization + jsonx round-trip) — CA5 COMPLETE

- codingagent/branch_summarization.go: branch-summarization.ts pure core —
  BranchSummaryResult/Details/Preparation shapes, CollectEntriesFor
  BranchSummary (old-leaf walk to the deepest common ancestor; compaction
  boundaries included so their summaries become context), getMessageFrom
  BranchEntry (tool results skipped; custom/branch/compaction entries
  projected), PrepareBranchEntries (two-pass: cumulative file ops from ALL
  entries incl. nested pi-generated branch summaries, then newest-to-oldest
  token-budgeted message collection with the 90% summary-over-budget rule),
  BranchSummaryPreamble/Prompt constants.
- **JSONX ROUND-TRIP FIX** (second upstream gap from session decode):
  jsonx.Obj had no MarshalJSON/UnmarshalJSON — encoding/json serialized it
  as {} (unexported fields), silently dropping ToolCall arguments in every
  session round-trip. Added ordered-key MarshalJSON + order-preserving
  UnmarshalJSON; jsonx suite and full repo green.
- decodeAgentMessage hardened: blocks inferred by shape when type tags are
  absent (Name -> toolCall, Thinking -> thinking, else text) and Content
  handled in both string and {IsText, Text, Blocks} marshal forms.
- Tests (+7, total 206): branch collection (disjoint paths / same
  position / no old leaf / chronological order), compaction inclusion,
  preparation (unlimited vs tight budget, tool-result skip, summary-over-
  budget 90% rule, nested file-ops accumulation incl. fresh tool calls),
  constants. Race clean.

## CA6 ROUND-1 STARTED (system prompt + session types)

- codingagent/system_prompt.go: system-prompt.ts — BuildSystemPromptOptions
  / normalized shape, SystemPromptSections (preamble untagged, others
  wrapped in same-named tags), section-name validation (lowercase
  [a-z][a-z0-9_-]* minus preamble), buildRules (dedup + tool-guideline /
  prompt-guideline merge + bash/powershell fallback rule suppressed when
  grep/find/ls present + concise/paths closers), tool snippet rendering
  ((none) when no snippets), docs section with the pi documentation map,
  addendum/project_context (<project_instructions path=...>) /skills
  (sorted, file-read-tool attribution)/cwd layers, custom sections,
  BuildSystemPromptState (forced prompts opaque), DiffSystemPromptSections
  (changed/added/removed-with-null patch semantics).
- codingagent/agent_session_types.go: agent-session.ts part 1 — defaults.ts
  (DefaultThinkingLevel medium + 7 options), ParsedSkillBlock +
  ParseSkillBlock (anchored regex, optional trailing user message), the
  session event vocabulary (14 discriminants incl. compaction lifecycle,
  auto/summarization retries, queue updates, bash execution deltas),
  CompactionReason, withoutDeletedHeaders (null-valued header deletion
  markers), AgentSessionConfig (settings-manager as a seam interface until
  its port), queued-input dispositions, ModelCycleResult/SessionStats.
- Tests (+14, total 220): system prompt defaults/custom/rules-dedup/fallback
  suppression/layers/custom-section battery incl. invalid names, forced
  state, section diffing (changed/unchanged/removed), skill-block parse
  (full/no-message/negative battery), thinking defaults, header filtering.
  Race clean.

## CA6 ROUND-2 DONE (settings-manager)

- codingagent/settings_manager.go: settings-manager.ts core — two-scope
  (global ~/.pi/agent/settings.json, project <cwd>/.pi/settings.json)
  storage with an in-process mutex for proper-lockfile (single-host
  deviation), Settings as generic maps so unknown keys round-trip,
  InMemorySettingsStorage, DeepMergeSettings (recursive object merge,
  project precedence), MigrateSettings (queueMode->steeringMode,
  websockets-bool->transport, skills-object->array+enableSkillCommands,
  retry.maxDelayMs->retry.provider.maxRetryDelayMs), SettingsManager with
  trust gating (untrusted project scope blanked, writes rejected, trust
  toggling reloads), load-error capture + DrainErrors, modified-field and
  modified-nested-field session tracking (only marked fields persist on
  save, nested keys merge into stored objects), ApplyOverrides (merged-view
  only), Reload, and the accessor surface (provider/model/theme with path
  filtering, thinking levels incl. per-model overrides, steering/follow-up
  defaults, session dir normalization, string-slice/bool/nested-compaction
  readers, changelog marker).
- Tests (+10, total 230): merge (project precedence, nested deep merge,
  global untouched), migration battery, trust (untrusted blanking, write
  rejection, trust-toggle reload cycles), file persistence round-trip,
  nested persistence (modified key merges, other fields survive),
  accessors (defaults, path-theme filtering, bool defaults), load errors
  (capture + drain + defaults still served), in-memory override layering,
  session-dir expansion, changelog marker. Race clean.

## CA6 ROUND-3 DONE (git discovery + project context files)

- codingagent/context_files.go: footer-data-provider.ts's findGitPaths
  (ordinary repos, linked worktrees via .git file pointers with commondir
  resolution, HEAD existence check, ancestor walk) + resource-loader.ts's
  context-file walk — loadContextFileFromDir (AGENTS.override.md >
  AGENTS.md > AGENTS.MD > CLAUDE.md > CLAUDE.MD, regular files only),
  findShadowedContextFile (nested linked worktrees under the main repo
  shadow the main copy — canonicalized comparison because git writes
  gitdir in realpath form while cwd may be symlinked; bare layouts and
  submodules excluded), LoadProjectContextFiles (global agent-dir context
  first, then cwd ancestors closest-to-root first, shadowed + duplicate
  paths skipped, BOM stripped).
- Tests (+8, total 238): git discovery (ordinary repo from nested dir,
  worktree file + commondir, none), context loading (global+cwd, candidate
  precedence incl. override, ancestor order, worktree shadowing via a
  real nested worktree layout, BOM strip). Race clean.

## CA6 ROUND-4 DONE (skills)

- codingagent/skills.go: skills.ts (509 lines) — Agent-Skills-spec name
  validation (length, charset, hyphen edges, consecutive hyphens) and
  description validation, directory discovery (SKILL.md marks a skill
  root with no recursion; otherwise root .md files with descriptions load
  and subdirs recurse for SKILL.md; dotfiles/node_modules skipped;
  symlink targets resolved; .gitignore/.ignore/.fdignore honored with
  root-relative prefix rewriting incl. negation/escape handling via the
  ported ignore package), name fallback to parent dir, source mapping
  (user/project/path -> synthetic source info), disabled-model-invocation
  flag, FormatSkillsForPromptXML (agentskills.io format, five-char XML
  escaping, read-vs-bash attribution, disabled skills excluded),
  LoadSkills (defaults agentDir/skills + .pi/skills then explicit paths,
  realpath dedup for symlinked duplicates, name collisions recorded with
  winner/loser paths, warnings for missing/non-md paths).
- Tests (+8, total 246): name validation table, skill-root short-circuit,
  root-markdown + nested discovery, name fallback, ignore-file exclusion,
  diagnostics (missing description skips, invalid name warns but loads),
  collision winner (user beats project) + symlink dedup, explicit paths
  (file load, missing + non-md warnings), XML prompt format (escaping,
  exclusion, attributions). Race clean.

## CA6 ROUND-5 DONE (prompt templates)

- codingagent/prompt_templates.go: prompt-templates.ts — ParseCommandArgs
  (bash-style single/double quotes; unclosed quotes swallow the remainder,
  matching the reference), SubstituteArgs ($N/$@/$ARGUMENTS positional,
  ${N:-default}/${@:-default}/${ARGUMENTS:-default} defaults,
  ${@:N}/${@:N:L} bash-style slicing with 0-as-1 and clamping; NO
  recursive substitution of value patterns), loadTemplateFromFile
  (frontmatter description or first-body-line fallback with 60-char
  truncation, argument-hint), non-recursive .md directory scan with
  symlink resolution, LoadPromptTemplates (global agentDir/prompts +
  .pi/prompts + explicit file/dir paths, user/project/local source-info
  mapping via under-path checks), ExpandPromptTemplate (/name args ->
  parsed + substituted when the name matches).
- YAML NOTE pinned by tests: bare [file] parses as an array (yaml.v3
  mirrors the reference npm yaml), so argument hints must be quoted in
  fixtures.
- Tests (+9, total 255): arg parsing (quotes/unclosed/whitespace),
  positional/all substitution, defaults battery, slices (from/length/
  zero-as-one/beyond/clamp), no-recursion pin, loading (frontmatter desc
  + argument hint, first-line fallback, non-md ignored, user/project
  scopes), explicit paths (file + dir + missing), slash expansion
  (positional/all/quoted/unknown/plain passthrough), description
  truncation. Race clean.

## CA6 ROUND-6 DONE (bash executor + stripAnsi)

- codingagent/bash_executor.go: bash-executor.ts (156 lines) —
  ExecuteBashWithOperations over pluggable BashOperations with streaming
  sanitization (StripAnsi -> SanitizeBinaryOutput -> CR removal per chunk),
  streaming UTF-8 decode holding partial runes across chunks, rolling
  2x-limit chunk buffer, temp-file spill once output passes DEFAULT_MAX_
  BYTES (earlier chunks replayed at open), tail truncation on completion,
  abort path producing a cancelled result (nil exit code) instead of an
  error. Plus utils/ansi.ts's StripAnsi (CSI/OSC/DCS sequences, BEL, C0
  controls — tab/LF/CR preserved; the first cut stripped \n/\t, fixed by
  tests).
- Tests (+7, total 262): stripAnsi battery (colors, hyperlink OSC,
  controls, whitespace preserved), basic execution, streaming chunks,
  sanitization (ANSI + NUL + CR), truncation spill (spill file holds the
  full output, returned text is the tail), cancellation (nil exit code),
  split-rune decode. Race clean.

## CA6 ROUND-7 DONE (AgentSession core state machine)

- codingagent/agent_session.go: agent-session.ts core — AgentSession over
  the ported agent.Agent: token-based Subscribe/unsubscribe (Go function
  values are not comparable; ordered token list + map), emit fan-out,
  Dispose (abort + invalidate + listener drop), tool registry (built-in
  read/bash/edit/write/ls/grep/find + custom tools, allow/denylist
  filters, prompt snippet/guideline collection), SetActiveToolsByName
  (unknown names ignored + prompt rebuild), system prompt plumbing
  (RebuildSystemPrompt merges resource-loader context files/skills/custom
  + append; SystemPrompt renders run-over-base options preamble-first
  then sorted tagged sections), steering/follow-up queues with queue_update
  events, QueueForNextTurn/DrainPendingNextTurn custom-message asides,
  run-state tracking (SetAgentRunActive emits agent_settled), abort
  request plumbing, turn index, thinking level (session-transcript record
  + event, no-op on same level), SetModel (auth check with the reference's
  "No API key for provider/model" error, transcript record, settings
  persistence, per-model -> global-default -> medium thinking resolution),
  CycleModel (scoped-models pool filtered to available else available
  pool, forward/backward wraparound, <2 pool returns nil). The extension
  runner rides ExtensionRunnerSeam (Go-native deviation); ResourceLoader
  rides a seam until its full port.
- Tests (+8, total 270): registry (defaults active, discovery tools
  registered, activation filters + prompt reflects tools), prompt sections,
  queuing (steer/follow-up + queue_update events + drain), next-turn
  asides, event fan-out (settled emission + unsubscribe stops delivery),
  thinking level (apply/record/event once/no-op), model management (no-
  auth error, single-model cycle nil, scoped cycle forward/backward with
  wraparound, transcript record), dispose. Race clean.

## CA7 ROUND-1 DONE (print mode + JSON events + output guard)

- codingagent/print_mode.go: modes/json-event.ts + modes/print-mode.ts +
  core/output-guard.ts — ToJsonEvent (message_update projection strips the
  cumulative assistant "partial" snapshots recursively; other events pass
  through), OutputGuard (mutex-serialized raw writes over injectable
  stdout/stderr — Go has no monkey-patched process.stdout so the takeover
  maps to writer separation), RunPrintMode (JSON mode: header line then
  one JSON event per line with the wire shape; text mode: last assistant
  message's text blocks, error/aborted -> stderr + exit 1; prompt cycle
  through the ported agent loop), session events now carry JSON tags for
  the wire shape (camelCase like the reference's serialized events).
- Tests (+7, total 277): text mode (output/exit 0), error mode (stderr +
  exit 1, no stdout), JSON mode (header line + agent_settled events),
  partial-stripping projection, passthrough, output guard writes, guard
  concurrency. Race clean.

## CA7 ROUND-2 DONE (RPC mode core)

- codingagent/rpc_mode.go: modes/rpc/jsonl.ts + rpc-types.ts + the core
  dispatcher of rpc-mode.ts — SerializeJsonLine (strict LF-only framing;
  U+2028/2029 stay inline), AttachJsonlLineReader (LF-only with CR trim
  and final unterminated line, detach support; NOT bufio.Scanner which
  splits on extra Unicode separators), RpcCommand/RpcSessionState/
  RpcResponse wire shapes, RpcDispatcher over AgentSession: prompt/
  steer/follow_up (queued dispositions), abort, clear_queue (both queues
  drained), get_state (thinking/streaming/modes/session id/name/message
  count), set_model (lookup + no-auth error), cycle_model, get_available_
  models, set/get thinking levels, steering/follow-up mode persistence,
  get_messages (role-tagged JSON), get_entries, get_tree, get_last_
  assistant_text (text-block extraction), set_session_name, invalid-JSON
  and unknown-command errors.
- sendPrompt now records the user message in both the session transcript
  and the agent transcript (print + RPC prompt paths).
- Tests (+11, total 288): prompting battery (dispositions, queue state,
  clear), get_state, set_model (missing-model error), thinking (apply +
  7 levels), messages/entries, last-assistant-text, session name,
  invalid/unknown commands, JSONL serialization (framing), line reader
  (LF/CR/final line). Race clean.

## CA7 ROUND-3 DONE (CLI args + cmd/openagent binary — RPC mode live end-to-end)

- codingagent/cli_args.go: cli/args.ts — the full flag table (provider/
  model/api-key/system prompts/mode/print/continue/resume/session family/
  models/tools/exclude/thinking/extensions/skills/prompt-templates/themes/
  list-models/tui-mode/verbose/approve/offline), -- separator (@files
  route to FileArgs), -p consuming an optional following message,
  comma-list trimming, thinking validation (warning diag) and mode/tui
  validation (error diags), unknown --flags collected into UnknownFlags
  (= form and value-or-bool consumption) for extension flags, single-dash
  unknowns error, PrintHelp. Plus the binary plumbing: Mode constants,
  SetupPrintSession (session+settings+runtime wiring), SetupRpcDispatcher,
  and Serve (synchronous JSONL loop over stdin).
- cmd/openagent/main.go: the binary entry — help/version, diagnostics
  (error exits 1), mode resolution (--mode over -p-implied text), print
  and RPC dispatch. RPC mode verified END-TO-END against the compiled
  binary: get_available_thinking_levels returns the wire response, invalid
  JSON returns the error envelope.
- BLOCKING FIX in AttachJsonlLineReader: the goroutine form returned
  before reading (main exited first, no output); now synchronous until
  EOF (detach retained for API parity).
- Tests (+13, total 301): parsing battery (basics, flags, mode valid/
  invalid/missing, print with/without message, @file args incl. after --,
  comma lists, thinking warning, unknown flags =/value/bool/single-dash,
  list-models pattern vs bare vs @, tui-mode validation, project trust
  tri-state, help content, thinking level validation). Race clean.

## CA8 ROUND-1 DONE (theme core)

- codingagent/theme.go: modes/interactive/theme/theme.ts core — the Theme
  token model (50 fg + 7 bg tokens), NewTheme with precomputed fg/bg ANSI
  sequences (ParseColor -> Foreground/BackgroundAnsi; "" -> terminal-
  default \x1b[39m/\x1b[49m and no concrete color), the fallback chain
  (scrollbarTrack<-muted, scrollbarThumb<-text, thinkingMax<-thinkingXhigh,
  searchMatchText<-text, searchMatchBg<-selectedBg), appearance detection
  from concrete-color luminance (declared JSON appearance wins), Colors()
  hex resolution, StyleText; ThemeJSON parse (BOM tolerant) + CreateTheme
  (fg/bg token split, name fallback to filename) + LoadThemeFromPath;
  registry (SetRegisteredThemes/GetAvailableThemes/WithPaths incl. custom
  themes dir) + activation (SetTheme by name with change callbacks,
  ActiveTheme/SetThemeInstance/OnThemeChange); ParseAutoThemeSetting
  ("light/dark" pairs, exactly one slash, both sides non-empty),
  ResolveThemeSetting (auto pair by terminal theme, invalid path -> none);
  WCAG relative luminance (0-255 channels, linearization with math.Pow —
  the chained-multiplication draft was WRONG, fixed against the
  reference's 2.4 exponent), COLORFGBG background parsing (last valid
  0-255 field scanning backwards) with luminance classification, fallback
  dark.
- Tests (+12, total 313): token construction + fallbacks, terminal-default
  tokens, appearance (detected bright-on-dark + declared light), JSON
  round trip (name/appearance/colors/bg), invalid JSON, name fallback to
  filename, registry, activation (change events once, unknown theme
  error), auto-theme parse table, resolve table, RGB classification
  (230-light/15-dark on 0-255 channels), env detection (COLORFGBG bg=0
  dark / bg=15 light / missing fallback). Race clean.

## CA8 ROUND-2 DONE (syntax highlighting)

- codingagent/syntax_highlight.go: utils/syntax-highlight.ts — the hljs
  span-scope rendering machinery: getScopeFromSpanTag (first hljs-* class
  wins, matching the reference's class="hljs-title function_" -> title),
  getScopeFormatter (exact -> dot-prefix -> dash-prefix fallback),
  getActiveFormatter (innermost-out, theme default), isSpanOpenTagStart,
  RenderHighlightedHtml (span open/close tracking with entity decoding
  incl. numeric &#x27;/&#39; forms), decodeHtmlEntityAt.
  DOCUMENTED DEVIATION: highlight.js -> regex-based longest-match
  tokenizers for the eager language subset (go, python, bash, javascript,
  typescript, rust, json with py/golang/sh/js/ts/rs aliases; keyword/
  string/comment/literal/number/variable/type/title scopes), auto-detect
  by best coverage with languageSubset priority. TOKENIZE FIX: plain runs
  between matches tracked separately from the scan cursor (the shared
  cursor dropped leading plain text).
- Tests (+10, total 323): scope rendering (keyword/title/string, class
  attr with multiple classes takes the first hljs-*), dot/dash fallbacks,
  entity decoding, theme default, Go highlighting (keyword/comment/
  string), alias table, Python (keyword/literal), auto-detect,
  unknown-language passthrough (PINNED: nil-rules tokenizer emits plain
  text), bash variables. Race clean.

## CA8 ROUND-3 DONE (export-html core)

- codingagent/ansi_to_html.go: export-html/ansi-to-html.ts — the 16-color
  palette, color256ToHex (cube 55+n*40, grayscale 8+(n-232)*10),
  escapeHtml (five specials), textStyle SGR state machine (reset/bold/
  dim/italic/underline with their 2x resets, standard/bright fg+bg,
  38;5;N and 38;2;R;G;B extended with param-skip, defaults 39/49),
  AnsiToHtml (span close-then-reopen per escape, inline CSS, HTML
  escaping of inter-escape text, trailing span close), AnsiLinesToHtml
  (div.ansi-line wrappers, &nbsp; for empties).
- codingagent/export_html.go: export-html/index.ts core — parseColorString
  (hex + rgb() forms), luminanceOf (WCAG), adjustBrightness (clamped
  multiply), deriveExportColors (light: card=base + warmed info; dark:
  page 0.7x + card 0.85x; invalid falls back to the reference defaults),
  GenerateThemeVars (sorted --key: value; custom properties + derived
  export vars), SessionData payload (header/entries/leafId/systemPrompt/
  tools), BuildSessionData, EncodeSessionDataBase64. Template-file
  rendering rides a seam until the vendor assets land.
- Tests (+14, total 337): ansi battery (plain, standard/bright colors,
  background, 256-color, grayscale index, rgb truecolor, style combos,
  dim, reset-span-count, HTML escaping), line wrappers, color256 table,
  export derivation (dark/light/invalid), theme vars, session-data build
  + base64 round trip. Race clean.

## CA8 ROUND-4 DONE (extension runner core)

- codingagent/extension_runner.go: extensions/types.ts Extension surface +
  extensions/runner.ts core — Extension (per-event handler lists, tools/
  commands/flags/shortcuts registration with first-wins aggregation,
  On/RegisterTool/RegisterCommand/RegisterFlag/RegisterShortcut
  accessors), ExtensionError reporting shape, LoadExtensionsResult,
  ExtensionRunner: HasHandlers, Emit (handler errors and PANICS contained
  and routed to error listeners, never propagated), OnError,
  Invalidate/StaleMessage (session-replacement staleness), Extensions,
  AllTools/AllCommands/AllFlags aggregation. DOCUMENTED DEVIATION:
  extension loading from TS modules (jiti) -> Go-native extensions
  registering handlers directly; the dispatch/registration contract is
  preserved.
- Tests (+7, total 344): emit fan-out (both extensions, unsubscribed
  no-op), error reporting (path/event/message + string form), panic
  containment (reported as errors, runner stays alive), invalidation,
  registrations (tools/commands/flags/shortcuts round-trip), first-wins
  aggregation, extension listing. Race clean.

## CA8 ROUND-5 DONE (git sources + html entities + diagnostics)

- codingagent/git_source.go: utils/git.ts — GitSource shape, splitRef
  (scp-like git@host:path@ref, URL forms via net/url with pathname
  rewrite, bare host/path), decodeForValidation + hasUnsafe
  GitInstallPart (traversal/backslash/leading-slash/slash gating),
  hostedGitInfoFromURL (regex with scheme/scp/user/project/ref groups;
  bare host defaults to github.com), buildGitSource validation (.git
  strip, >=2 path segments, safety), ParseGitUrl (git: prefix strip,
  scheme gate — PINNED REFERENCE SEMANTIC: bare host/path without a
  scheme or prefix returns nil; hosted candidates with-ref-first then
  raw, https://-prefixed fallbacks, @-in-project rejection when a ref
  was split).
- codingagent/html_diagnostics.go: utils/html.ts + core/diagnostics.ts —
  DecodeHtmlEntity (named five + hex/decimal numeric with range
  validation), DecodeHtmlEntityAtHtml (semicolon search bounded at 16
  chars), ResourceCollision/ResourceDiagnostic shapes +
  FormatResourceDiagnostic (collision form with winner/loser, pathed and
  bare diagnostics).
- Tests (+10, total 354): git battery (bare-with-prefix, ref pinning,
  scp-like under prefix, git: prefix, full https, negative table,
  traversal rejection), entity decoding (named five, hex/decimal/emoji,
  range and unknown rejection, at-index with unterminated/overlong),
  diagnostic formatting (collision/warning/bare). Race clean.

## CA8 ROUND-6 DONE (package manager core)

- codingagent/package_manager.go: package-manager.ts settings + resolution
  core — PackageSource parsing (npm: prefix, git URLs via ParseGitUrl,
  local paths via IsLocalPath), PackageManager over the settings manager:
  baseDirForScope (agentDir/packages vs cwd/.pi/packages),
  installDirFor (npm/<spec>, git/<host-path[-@ref]>), GetInstalledPath
  (local resolves from the scope base; installed checked via stat),
  AddSourceToSettings/RemoveSourceFromSettings (scope-aware, dedup +
  absent-report), ListConfiguredPackages (sorted, install state),
  Resolve (project-first so cwd wins collisions, pi-manifest-driven
  extension/skill/prompt/theme resource listing with full PathMetadata
  incl. the new PackageRoot field, missing sources routed through the
  onMissing callback with skip semantics; network install lands with the
  runtime phase), SetProgressCallback + emitProgress.
- Tests (+7, total 361): source parsing (npm spec/git/local/empty),
  settings add/remove (dedup, project-global separation, absent remove),
  install paths (npm directory, missing, absolute local), manifest
  resolution (extensions + skills with metadata/packageRoot/enabled,
  empty kinds), missing-source callback, configured listing with install
  state, progress events. Race clean.

## CA10 ROUND-1 DONE (PARITY audit tables + module docs)

- PARITY.md: replaced the CA1/CA2–CA9 placeholder rows with the real
  audit — tui table (12 rows incl. the 3 NAPI deviation rows), CA2–CA9
  phase-status table (CA2–CA5 + CA7 done; CA6/CA8 partial with named
  remaining halves; CA9 pending), documented-gap list (LLM-call halves,
  cache-warmer, remote-catalog/radius slots).
- docs/modules/tui.md + tui.zh.md: the tui module reference page (colors,
  segmentation, keys/components/screens, known limitations) — paired
  English/Chinese per the docs standard.
- docs/modules/codingagent.md + codingagent.zh.md: the codingagent module
  reference page (configuration, model stack, sessions, tools,
  compaction, modes, extensions, known limitations) — paired.
- docs/architecture.md + .zh.md: module map rows now link the two new
  module pages (same-commit rule).
- Gates: all 7/7 green incl. verify-doc-pairs (29 pairs) and
  verify-md-links.

## CA8 ROUND-7 DONE (interactive helpers + auth guidance + slash commands)

- codingagent/interactive_helpers.go: interactive-mode.ts helper surface —
  FormatCrashExtensionHint (1/2/N-list grammars, whitespace-only entries
  filtered), IsAnthropicSubscriptionAuthKey (sk-ant-oat prefix) +
  AnthropicSubscriptionAuthWarning, IsUnknownModel placeholder detection,
  QuoteIfNeeded (shell-safe charset pass-through, single-quote escaping),
  FormatResumeCommand (TTY/persisted/file gates, --session-dir for
  non-default dirs, quoted session id), HasDefaultModelProvider,
  LlamaCppPostLoginGuidance, GetLoginProviderCompletionOptions (by-id
  dedup, auth-type ordering oauth<api_key, name sort),
  FormatAuthSelectorProviderType, GetLoginProviderSearchText,
  FormatLoginProviderCompletionDescription.
- codingagent/auth_guidance.go: auth-guidance.ts — GetProviderLoginHelp
  (docs paths), FormatNoModels/NoModelSelected/NoApiKeyFound messages
  with unknown-provider display swap.
- codingagent/slash_commands.go: slash-commands.ts — the 24-entry
  built-in catalog with argument hints.
- Tests (+13, total 374): crash hint grammar battery, subscription key,
  unknown model, quoting (safe/empty/space/quote-escape), resume command
  (no-message/non-TTY/full hint), default provider, llama guidance,
  login provider options (dedup/order/sort), auth type labels, search
  text, completion description, auth guidance battery, slash catalog.
  Race clean.

## CA8 ROUND-8 DONE (app keybindings)

- codingagent/keybindings_app.go: core/keybindings.ts — UseWindows
  Keybindings (win32 + WSL_DISTRO_NAME/WSL_INTEROP), AppKeybindings
  catalog (the full app.* table layered over the ported tui registry
  with platform overrides: undo ctrl+z/alt+z/ctrl+-, prompt jump
  windows-single vs dual-key, search ctrl+f vs ctrl+shift+f, model
  backward alt+p vs shift+ctrl+p, follow-up ctrl+q vs alt+enter, dequeue,
  paste-image, tree fold/unfold darwin-first ordering, suspend
  windows-empty), the 58-entry legacy KEYBINDING_NAME_MIGRATIONS map
  (short names -> qualified tui.editor./tui.input./tui.select./app.
  bindings), ToKeybindingsConfig (string-or-string-array coercion,
  mixed/typed arrays rejected), LoadUserKeybindings (keybindings.json
  with BOM strip, legacy migration, broken-JSON empty config).
- Tests (+6, total 380): platform detection table, catalog essentials +
  no-default commands, platform overrides (undo/search value sets),
  migration map, config coercion (single/multi/mixed/type-rejected),
  user loading (missing/legacy-migration/broken). Race clean.

## CA8 ROUND-9 DONE (clipboard + browser opener)

- codingagent/clipboard_browser.go: utils/clipboard.ts + open-browser.ts
  — isRemoteSession (SSH_CONNECTION/SSH_CLIENT/MOSH_CONNECTION), isWSL,
  emitOsc52 (base64 payload, 100k encoded cap), runClipboardCommand
  (timeout-guarded exec with stdin piping),
  ReadClipboardText (linux chain: termux-clipboard-get, wl-paste,
  xclip, xsel), CopyToClipboard (darwin pbcopy / windows clip / linux
  wl-copy+xclip+xsel+termux chains, WSL Windows-Terminal OSC 52
  preference, remote/headless OSC 52 routing, oversized detection, the
  reference's actionable per-environment error messages), OpenBrowser
  (darwin open / windows rundll32 — never a shell so cmd.exe
  metacharacters cannot re-parse URLs — / xdg-open, best-effort with
  swallowed launcher failures), KeyDisplayText (first default key for
  hints). NAPI native clipboard deviation: OSC 52 + platform commands.
- Tests (+7, total 387): remote/WSL detection tables, read contract,
  copy error (actionable message or host-helper success), OSC 52 emit
  (prefix/suffix capture + oversized rejection), browser no-crash, key
  display (defaults/no-defaults/unknown). Race clean.

## CA8 ROUND-10 DONE (tool HTML renderer)

- codingagent/tool_renderer.go: export-html/tool-renderer.ts —
  isBlankRenderedLine (ANSI-stripped emptiness), trimRendered
  ResultLines (leading/trailing blank trim), ToolHtmlRenderer with
  render-call/result seams over tool definitions (panic recovery to
  empty, collapsed-omitted-when-identical semantics),
  TemplateRenderedTools (bash/read/write/edit/ls rendered by the HTML
  template), PreRenderCustomTools (assistant toolCall blocks with
  non-template names get call HTML; toolResult entries attach
  collapsed/expanded to existing or create new entries).
- Tests (+6, total 393): blank-line trim (mixed ANSI + all-blank),
  renderCall (styled output, unknown tool, panic recovery), renderResult
  (collapsed/expanded, identical-omission), PreRenderCustomTools (custom
  call+result round trip, template-only skipped, nil renderer). Race
  clean.

## CA8 ROUND-11 DONE (crash log)

- codingagent/crash_log.go: core/crash-log.ts — CrashRecord (bounded at
  5, 7-day age window), ReadCrashLog (field-validated parse, empty on
  failure), writeCrashLog (indented + newline), normalizeStackPath,
  stackContainsPath (descendant mode via suffix-slash; exact mode
  requires a delimiter — colon/paren/whitespace — so path prefixes of
  longer paths do not match; Windows drive paths compare
  case-insensitively), FindExtensionStackMatches (stack frames filtered
  to "at " lines with URI decoding and forward-slashing; package-root
  descendants, directory entries via /index.[cm][jt]s detection, exact
  file fallback; package-origin labels use the source, others the
  path; dedup), RecordCrash (best-effort, newest-kept window),
  TakeUnnotifiedCrash (newest unnotified within the age window; all
  pending marked), ClearCrashLog.
- Tests (+10, total 403): read (missing/broken), record (fields/
  persistence/window bounding), take-unnotified (old-vs-recent + no
  resurfacing + empty), clear, stack matching (directory entry, exact
  file, no-match, empty stack, package-root descendants, prefix-
  delimiter semantics), path normalization. Race clean.

## CA8 ROUND-12 DONE (cache stats + experimental gate)

- codingagent/cache_stats.go: core/cache-stats.ts — CacheTTLMS (5-minute
  Anthropic default), noise floor (1024 tokens), CacheMiss (missed
  tokens/cost, idle ms, model-changed), detectMiss (zero-cache turns
  only count when cache activity was reported — distinguishing
  cache-read-only providers from no-cache providers; miss = previous
  prompt minus cacheRead, extra cost = paid rate (input+cacheWrite incl.
  write premium) vs. cache-read rate with model-price fallback),
  asPreviousRequest (sticky reportedCache), scanCacheEntries (compaction
  and branch summaries RESET the previous request — the next prompt is
  new content, not re-billed; model switches are NOT exempt; cache_warm
  usage entries seed the state; assistant messages detect + advance),
  ComputeCacheWaste / CollectCacheMisses (keyed by assistant message for
  transcript notices) / DetectCacheMiss (just-completed probe).
  Plus core/experimental.ts's AreExperimentalFeaturesEnabled
  (PI_EXPERIMENTAL == "1").
- Tests (+9, total 412): no-miss (full hit), miss (tokens + cost),
  noise floor, provider-without-cache exclusion, compaction reset, model
  change counts, per-message collection, just-completed probe (idle ms,
  model flag), experimental gate table. Race clean.

## CA8 ROUND-13 DONE (http dispatcher + footer git helpers)

- codingagent/http_dispatcher.go: core/http-dispatcher.ts —
  DefaultHTTPIdleTimeoutMS (5 min), the five-choice settings table,
  ParseHttpIdleTimeoutMs ("disabled" case-insensitive -> 0, numeric
  strings, float flooring, NaN/negative/typed rejection),
  FormatHTTPIdleTimeoutMs (label lookup with N-sec fallback),
  ApplyHTTPProxySettings (seeds HTTP_PROXY/HTTPS_PROXY only when unset),
  ConfigureHTTPDispatcher (validation + applied-timeout record; Go's
  transport tuning owned by the ai http seam — documented mapping).
- codingagent/context_files.go (footer-data-provider.ts additions):
  ResolveBranchWithGit (symbolic-ref --quiet --short, "" on detached or
  unavailable), IsWSLEnvironment, IsWindowsMountedRepoPath (/mnt/<drive>
  pattern), ShouldPollGitHead (WSL + mounted).
- Tests (+6, total 418): timeout parsing table (disabled/numeric/padded/
  floor/zero/negative/NaN/empty/typed), formatting (label + custom),
  proxy seeding (empty no-op, set, no-overwrite), dispatcher validation,
  branch resolution (valid repo contract + non-repo empty), mounted-path
  pattern. Race clean.

## CA8 ROUND-14 DONE (model search + catalog refresh coordinator)

- codingagent/model_search.go: modes/interactive/model-search.ts —
  GetModelSearchText (catalog corpus: id-leading),
  GetModelSelectorSearchText (selector corpus: provider-leading so exact
  provider-prefixed queries rank before proxy-provider ids like
  openrouter/openai/gpt-5; bare id demoted from the leading position).
- codingagent/model_catalog_refresh.go: model-catalog-refresh.ts — the
  ModelCatalogRefreshCoordinator sharing one operation per runtime while
  each caller's cancellation stays independent (entry created on first
  waiter, done-channel broadcast, last-waiter release via cancel;
  synchronous Refresh in the Go port, documented mapping),
  RefreshModelCatalogs package function.
- Tests (+4, total 422): search corpus (id-leading + canonical + name;
  no trailing space bare), selector corpus (provider-leading + demoted
  id), coordinator entry contract, RefreshModelCatalogs smoke (fresh
  runtime, no network). Race clean.

## CA8 ROUND-15 DONE (bug report core)

- codingagent/bug_report.go: core/bug-report.ts (redaction + bundle) —
  isSensitiveKey (camelCase split before the api-key/secret/token/
  password/credential/authorization/cookie boundary pattern),
  RedactUrl (nested-scheme recursion, credential stripping, sensitive
  query-param redaction, clean URLs unchanged), RedactJsonValue (deep
  copy with key redaction + string URL stripping via a typed walk),
  RedactSettings (trackingId dropped, remainder redacted),
  CollectBugReportEnvironment (version/userAgent/runtime/platform/arch/
  shell basename, terminal context TERM/PROGRAM/COLORTERM/TMUX/SSH/CI,
  PI_* env NAMES ONLY sorted — values never leave),
  BugReportBundle + BugReportFiles (report.json + diagnostics.json
  always, session.jsonl and summary.md when present, trailing-newline
  idempotence), BugReportArchiveFileName. Archive writing and the LLM
  summary land with the runtime phase.
- Tests (+7, total 429): sensitive-key table (13 in / 8 out incl.
  camelCase and boundary cases), URL redaction (credentials, query
  params, clean passthrough, non-URL), JSON redaction (nested maps,
  arrays, URL strings, safe keys), settings (trackingId dropped),
  environment (fields + PI_* names-only), files (minimal/full/newline
  idempotence), archive name. Race clean.

## CA8 ROUND-16 DONE (keybinding hints + external editor + session share)

- codingagent/keybinding_hints.go: keybinding-hints.ts + external-
  editor.ts — FormatKeyText (/ alternatives, + parts, darwin alt ->
  option, capitalize), KeyTextOf/keysOfBinding over the app catalog,
  RawKeyHint (dim key + muted description); EditInExternalEditor (temp
  dir, command split with the file appended, inherited stdio, BOM strip
  + single trailing newline trim, best-effort cleanup).
- codingagent/session_share.go: session-share.ts core —
  CreateShareTrailingEntries (pi.share custom entry carrying the system
  prompt + tool schemas, 8-char id, branch-leaf parent),
  ExportSessionForShare (GetEntries JSONL + trailing entry),
  GhCLIAvailable/GhCLIStatus (auth check with the actionable login
  error), ShareViaGist (gh gist create --filename with stdin content).
  Radius upload and HTML export ride the runtime seams.
- AgentSession gained registeredTools (name -> RegisteredTool) so
  share/registry code can reach definitions; NIL-MAP INIT FIX found by
  tests.
- Tests (+7, total 436): key text (plain/capitalized/alternatives with
  darwin option mapping/single), KeyTextOf (defaults/no-defaults),
  raw hint (dim escape), external editor (true/false/missing), trailing
  entry shape (custom type, parent, id length, data), share export
  (message-first JSONL + trailing pi.share parented at the branch leaf),
  gh helpers smoke. Race clean.

## CA8 ROUND-17 DONE (session export + zip archive)

- codingagent/session_export.go: core/session-export.ts +
  utils/zip.ts — SerializeSessionBranch (fresh session header with the
  current id/cwd, branch entries re-parented into a linear export chain,
  optional TrailingEntries callback receiving the branch leaf and a
  shared timestamp), ExportSessionToJsonl (default filesystem-safe
  timestamped name, nested-dir creation, path resolution against the
  process cwd), CreateZipArchive/WriteZipArchive (classic ZIP: 30-byte
  local + 46-byte central headers, DOS date/time encoding with the 1980
  clamp, raw-deflate method 8, CRC-32, EOCD with entry counts and
  offsets) + dosDateTime + deflateRaw. Verified round-trippable by
  archive/zip.
- Tests (+6, total 442): serialize (header fields, null-then-chained
  parentIds), trailing callback (leaf parent + timestamp), export
  (nested path, trailing newline, default name), archive signature,
  full zip round trip through archive/zip (member names + content),
  DOS date encoding (bit layout + pre-1980 clamp). Race clean.

## CA8 ROUND-18 DONE (fs-watch + wsl + child-process utils)

- codingagent/process_utils.go: utils/fs-watch.ts + wsl.ts +
  child-process.ts — FSWatchRetryDelayMS, WatchWithErrorHandler (stat-
  poll loop at the reference's watcher cadence — fsnotify not a
  dependency, documented mapping — with Close/CloseWatcher idempotence),
  IsWSLFull (WSL_DISTRO_NAME/WSLENV markers + /proc/version microsoft
  regex), SpawnProcess/SpawnProcessSync (LookPath resolution, exit-code
  capture), WaitForChildProcess (the exit-stdio GRACE PROTOCOL: after
  exit the idle timer re-arms on every chunk so an actively writing
  descendant keeps the read going while a quiet inherited handle
  releases after 100ms — the pi#5303 fix). GetPiUserAgent already owned
  by paths_text.go.
- Tests (+5, total 447): watcher change detection (modify -> event),
  close idempotence + nil safety, WSL detection smoke, spawn sync
  (success output/failing/missing), grace protocol (immediate-exit child
  with open pipes completes without hanging, stdout captured). Race
  clean.

## CA8 ROUND-19 DONE (trust manager + session-cwd guards)

- codingagent/trust_manager.go: core/trust-manager.ts — normalizeCwd +
  findNearestTrustEntry (ancestor walk), GetProjectTrustParentPath,
  HasTrustRequiringProjectResources (.pi settings.json/extensions/
  skills/prompts/themes/SYSTEM.md/APPEND_SYSTEM.md + ancestor
  .agents/skills excluding the user's own home skills dir),
  ProjectTrustStore (trust.json with buffered mutex locking — the
  proper-lockfile deviation — Get/Set/SetMany, nil clears).
- codingagent/pi_manifest_trust.go: core/project-trust.ts +
  session-cwd.ts — FormatProjectTrustPrompt (app name + config dir),
  ResolveProjectTrusted (override -> no trust-requiring resources ->
  extension decision with remember -> stored nearest-ancestor -> default
  always/never/ask -> headless-denied UI prompt with remember),
  GetMissingSessionCwdIssue (persisted file + stat check),
  FormatMissingSessionCwdError/Prompt, MissingSessionCwdError +
  AssertSessionCwdExists. PiManifest/ReadPiManifest and the thinking
  defaults were already owned by event_bus.go/agent_session_types.go.
- Tests (+7, total 454): store round trip (unrecorded/record/flip/
  clear), nearest-ancestor inheritance, parent path (+ root), resource
  detection (bare/settings.json/ancestor-skills), missing-cwd issue
  (valid/vanished/error+prompt forms/assert), resolution order (override
  both ways/clean-dir/stored/default-policy/headless/UI-prompt +
  remember), trust prompt text. Race clean.

## CA8 ROUND-20 DONE (provider attribution + telemetry + diagnostics)

- codingagent/provider_attribution.go: core/provider-attribution.ts —
  host matchers (OpenRouter suffix-or-exact host, NVIDIA NIM,
  Cloudflare API + AI Gateway, OpenCode),
  getDefaultAttributionHeaders (install-telemetry-gated OpenRouter
  HTTP-Referer/X-OpenRouter-Title/Categories, NIM X-BILLING-INVOKE-
  ORIGIN, Cloudflare pi user agent), getSessionHeaders (OpenCode
  session/client routing), MergeProviderAttributionHeaders (session +
  attribution under caller sources, nil when empty).
- codingagent/runtime_services.go: telemetry.ts + settings-diagnostics.ts
  — isTruthyEnvFlag (1/true/yes), IsInstallTelemetryEnabled (env
  override over the setting), CollectSettingsDiagnostics (pathed vs
  scope messages as warnings), DeduplicateDiagnostics (type+message key,
  first occurrences — startup and runtime managers report the same file
  error). RuntimeCredentials was already owned by auth_storage.go; the
  SettingsManager gained Get/SetEnableInstallTelemetry (default true,
  global-scope persistence).
- Tests (+6, total 460): attribution battery (OpenRouter set + telemetry
  gate + unattributed, NIM/Cloudflare by id and host, gateway host,
  OpenCode session routing + caller precedence + no-leak), telemetry
  gate (default/env override both ways/setting flip), diagnostics
  collection (pathed/scope/warning type), dedup (type-differentiated).
  Race clean.

## CA8 ROUND-21 DONE (radius + clipboard command)

- codingagent/radius_models_store.go: core/radius.ts — RadiusProviderID,
  EnvRadiusGateway, DefaultRadiusGateway, NormalizeRadiusGatewayUrl
  (trailing-slash strip, scheme defaulting, empty -> production),
  GetRadiusGatewayUrl (env override honored). The models-store.ts port
  was already owned by auth_storage.go (FileModelsStore +
  InMemoryCodingAgentModelsStore).
- codingagent/abort_clipboard_command.go: utils/clipboard-command.ts —
  RunClipboardCommand (daemonization-safe: no retained stderr, stdin
  only when provided, kill-once timeout guard, 50MB default buffer cap
  with oversized abort, nil = failure / empty buffer = success). The
  abort.ts port was already owned by utils_core.go.
- Tests (+7, total 467): gateway normalization table (8 cases incl.
  schemeless + trailing slashes + empty), default origin, env override
  (trimmed + schemeless), clipboard runner (success output, false +
  missing failures, sleep timeout honored under 2s, oversized buffer
  rejected). RACE FIX: the timeout-abort flag moved from once.Do bool to
  atomic.Bool (timer-goroutine write vs post-wait read); 3x -race clean.

## CA8 ROUND-22 DONE (interactive-mode driver + binary wiring) — LOOP BREAK

COURSE CORRECTION: rounds 2-21 ported leaf utilities while the core interactive-mode deliverable was deferred. This round attacks the driver itself.

- codingagent/interactive_mode.go: modes/interactive/interactive-mode.ts
  DRIVER CORE — InteractiveModeOptions, NewInteractiveMode (renderer
  choice regular TuiMainScreen / fullscreen TuiAltScreen over the ported
  tui package, keybindings manager over AppKeybindings +
  LoadUserKeybindings, layout root chat/status/editor stacks with a
  bordered editor, focus wiring, OnSubmit dispatch), Run (startup
  diagnostics by severity + model fallback + unnotified crash notice,
  TUI lifecycle, shutdown handoff), handleEditorSubmit implementing the
  REFERENCE DISPATCH ORDER (/quit → /resume seam → bash !/!! with
  running-guard → compaction queue → streaming steer → session
  prompt),
  dispatchSlashCommand (wired handlers + catalog seam + unknown-command
  warning), handleBashCommand (executor over local shell operations,
  cwd from process, !! excluded from context), /session stats, /name
  (AppendSessionInfo), showStatus/showWarning/showError with a test
  sink, requestShutdown idempotence.
- codingagent/tty_{unix,windows,probe_darwin,probe_linux}.go:
  IsStdinATTY — termios ioctl probe (the char-device bit alone
  misclassifies /dev/null; windows falls back to device bit + NUL
  exclusion).
- cmd/openagent/main.go + cli_args.go: --mode interactive wired (mode
  constant, validation list, help text, TTY gate with the loud error,
  session construction, driver launch). BINARY VERIFIED: --mode
  interactive < /dev/null exits 1 with "interactive mode requires a TTY
  on stdin".
- Tests (+9, total 476): construction (TUI/editor/keybindings), /quit
  idempotent shutdown, unknown-command warning, catalog seam, /resume
  seam, bash dispatch (echo through the executor), prompt (message
  persisted to session), /session stats, /name set + report, startup
  diagnostics battery. Race clean. 30 packages green, gates 7/7.

## CA8 ROUND-23 DONE (footer + timer/loader/border components)

- codingagent/footer.go: components/footer.ts data surface —
  SanitizeStatusText (control chars -> spaces, collapse), FormatTokens
  (1.0k/10k/1.0M/10M boundaries), FormatCwdForFooter (~-relative with
  outside-home passthrough), UsageTotals + usageEntryFields +
  addUsageToTotals, Footer.ComputeUsageTotals (usage entries, assistant
  + toolResult messages with the usage record extracted from the
  nested Usage key — flat-parse missed it, tests caught it — plus
  branch summaries and compaction; cache hit rate from the latest
  assistant prompt), RenderLines (pwd + git branch + session name;
  ↑/↓/R/W tokens, CH% hit rate, $cost with the (sub) marker for
  kimi-coding/subscriptions, ?/window (auto) context display).
- codingagent/interactive_components.go: components/countdown-timer.ts
  (1s ticks, synchronous first tick, dispose idempotence),
  bordered-loader.ts (over the ported tui Loader with Start/Stop/
  SetMessage), dynamic-border.ts (active/inactive style swap).
- Tests (+9, total 485): sanitize, token boundaries (7-case table), cwd
  home-relative (inside/home/outside/no-home), usage totals (sums +
  cost + 98.4% hit rate), render lines (pwd + branch + name, stats
  battery, subscription marker), countdown (initial tick + expiry
  within window), dispose (no post-dispose expiry, idempotent),
  dynamic border. Race clean.

## CA8 ROUND-24 DONE (assistant-message component)

- codingagent/assistant_message_component.go: components/assistant-
  message.ts — AssistantMessageComponent over the ported tui stack:
  OSC 133 shell-integration zones (A start / B+C end) around text-only
  renders, skipped when tool calls are present; UpdateContent with
  text blocks (trimmed markdown), CONSECUTIVE THINKING GROUPING (runs
  joined with blank lines, spacing only when visible content follows —
  avoiding the superfluous blank line before tool-execution blocks),
  per-run visibility overrides over the global hide toggle (Toggle
  ThinkingVisibility; SetHideThinkingBlock clears overrides),
  SetHiddenThinkingLabel/SetOutputPad re-render, empty-content
  handling.
- Tests (+7, total 492): text-only (zone prefixes + content), tool
  calls (no zones), thinking grouping (both blocks + answer), hidden
  thinking (label shown, content hidden, per-run reveal, global
  re-hide clearing overrides), label swap, empty content, update
  content (retain + re-render v1 -> v2). Race clean.

## CA8 ROUND-25 DONE (custom message/entry + markdown transform)

- codingagent/custom_components.go: components/markdown-transform.ts +
  custom-message.ts + custom-entry.ts — CreateMarkdownTransform
  (transformer chain with PANIC CONTainment keeping the current
  markdown, empty-return keeps current, context carries message type/
  streaming/width), CustomMessageView/CustomEntryView render-side views
  (messages.go/session_entries.go own the persisted shapes),
  CustomMessageComponent (custom renderer first owning its styling with
  panic fallthrough to the boxed label + markdown default, expanded/
  outputPad re-renders), CustomEntryComponent (renderer error boxed
  with the [type] renderer-failed message, nil renderer renders
  nothing).
- Tests (+8, total 500): transform chain (upper + suffix, panic
  containment, empty-keep, context pass-through), custom message
  (default box label + content, custom renderer with expansion state +
  re-render on toggle, renderer panic falls to default, outputPad),
  custom entry (render + expansion toggle, error box, nil renderer
  blank). Race clean.

## CA8 ROUND-26 DONE (diff renderer)

- codingagent/diff_component.go: components/diff.ts — parseDiffLine
  (prefix + optional line number + content; RE2-safe class after the
  \s and +- range panics), replaceTabs (3-space expansion),
  renderIntraLineDiff (the ported diff package's whitespace-grouping
  tokenizer drives word changes; inverse sequences wrap changed tokens;
  leading whitespace stripped from the FIRST removed/added part so
  indentation stays unhighlighted), RenderDiff (context passthrough,
  removed-run then added-run collection with intra-line pairing ONLY on
  exactly-one-removed + exactly-one-added — a single-line modification
  — otherwise plain runs, standalone added lines, tab expansion).
- Tests (+8, total 508): parse table (added/removed/context/
  unparseable), tab expansion, context render, standalone added,
  single-line modification (inverse present, common prefix plain),
  multi-line change (no pairing, run order), unparseable passthrough,
  tabs in output. Race clean.

## CA8 ROUND-27 DONE (summary components + markdown nil-theme fix)

- codingagent/summary_components.go: components/branch-summary-
  message.ts + compaction-summary-message.ts — SummaryMessageComponent
  sharing the custom-message background: bold [branch]/[compaction]
  label, collapsed hint with the expand key, expanded markdown header
  (Branch Summary / Compacted from N tokens), SetExpanded/Toggle,
  locale-formatted token counts.
- tui/markdown.go FIX: NewMarkdown now identity-defaults nil theme
  functions (Bold/Italic/Strikethrough/Underline) — a zero-value
  MarkdownTheme panicked on styled (bold) renders; found by the
  expanded-summary tests.
- Tests (+7, total 515): branch collapsed (label + hint, content
  hidden), expanded (header + content), toggle both ways, compaction
  collapsed (52,300 locale format, content hidden), expanded (1,000
  tokens header + content), locale int table. tui race clean too.

## CA8 ROUND-28 DONE (tui race-hardening sweep)

Product fixes (found by running tui under -race, newly added to the verification set):
- tui/scroll_view.go: a single stateMu now guards the scroll/layout/ scrollbar fields (contentHeight, viewport, scrollTop, following, transient visibility, hide timer) across ScrollTo/ScrollBy/ ScrollToStart/ScrollToEnd/UpdateLayout/SetScrollbar/ SetScrollbarActive; markScrollbarActivity and hideTransientScrollbar split into locked (caller holds) + acquiring variants after TWO self-deadlocks (UpdateLayout -> hide; ScrollBy -> mark) surfaced as test timeouts.
- tui/tui.go + box_loader.go: the Container/Box mouseLayout caches are mutex-guarded (the alt-screen stop hook renders a final frame on the caller goroutine while the scheduler frame may still be in flight).
- tui/markdown.go (from round 27): nil theme functions identity-
  defaulted — zero-value MarkdownTheme panicked on styled renders.

Test-side fixes: render_test.go linesComponent gained a mutex + setLines/appendLine (18 raw mutation sites rewrote), inputLinesComponent renderCount guarded, virtualTerminal.writesCount added (raw len(terminal.writes) raced the writer), negotiationHarness inputs mutex + pointer receivers (the long-queued PORTING round-8 negotiation-replay flake), and the input-preempts-throttled-frame assertion accepts the preempt count or more (timing-dependent).

All green: 30 packages, gates 7/7, codingagent -race, tui -race (newly clean).

## CA8 ROUND-29 DONE (bash execution component)

- codingagent/bash_execution_component.go: components/bash-execution.ts
  — StripAnsiCodes (CSI/OSC/SGR classes), BashExecutionComponent with
  the bordered command header, running loader (nil-UI static-text
  fallback — the animated Loader requires a TUI for its render hook),
  AppendOutput (ANSI strip + CRLF/CR normalization + incomplete-line
  continuation joins), SetComplete (complete/error-by-exit-code/
  cancelled statuses, loader stop, full-output path), preview collapse
  (last 20 logical lines with the hidden-count hint) vs expanded, and
  availableLines over the shared tail-truncation defaults.
- Tests (+11, total 526): ANSI strip table (SGR/OSC/plain), initial
  state (header + loader), append (basic, incomplete-line join, ANSI
  stripped), complete (footer + loader removal), error status (exit
  code 1), cancelled, preview hidden hint + expanded reveal, full path
  note, to-text. Race clean.

## CA8 ROUND-30 DONE (tool execution component)

- codingagent/tool_execution_component.go: components/tool-execution.ts
  — ToolRenderers (the interface the component needs from a tool: how
  to draw it, neither executing nor reading schemas; renderShell
  default-boxed vs self-framed), ToolExecutionComponent with fallback
  rendering (bold tool name pending; result text under the 10-line
  preview with the more-lines hint; expanded shows all),
  SetArgs/SetExecutionStarted/SetArgsComplete/SetPartial streaming
  transitions, SetResult switching to the result renderer, IsError.
- Tests (+7, total 533): fallback pending, result preview (25 lines ->
  hint, no overflow; expanded reveals), short result (no hint), custom
  renderers (default shell call->result swap, self shell), error flag,
  state transitions render validity. Race clean.

## CA8 ROUND-31 DONE (model selector component)

- codingagent/model_selector_component.go: components/model-selector.ts
  — ModelSelectorComponent with scoped/all scope (Tab toggle gated on
  scoped items), fuzzy filtering over GetModelSelectorSearchText
  corpora with DEFAULT-FIRST PINNING on "default" queries (defaults
  hoisted ahead of fuzzy matches, dedup by provider+id), cursor-to-top
  on queries vs clamped restore on clear, wrap navigation (up wraps to
  bottom, down to top), enter/escape/ctrl+s dispatch (select, cancel,
  save-as-default), typed-character query extension + backspace
  shrink, the visible 10-row window with → cursor / ✓ current /
  [provider] / · default badges, scroll counter, and no-results text.
- Tests (+10, total 543): initial list (ids + provider badges), scope
  toggle round trip, search (3-char filter, exclusion, backspace
  restore), no results, navigation wrap both ways, select, cancel,
  save-default, default pin + badge, current marker. Race clean.

## CA8 ROUND-32 DONE (login dialog component)

- codingagent/login_dialog_component.go: components/login-dialog.ts —
  LoginDialogComponent with the titled frame (provider-name and title
  overrides), ShowAuth (OSC 8 hyperlinked URL + platform Cmd/Ctrl
  click hint + instructions + browser open), ShowDeviceCode
  (verification URL link + user code), ShowManualInput/ShowPrompt
  (pending-submit resolution on enter, "> value" echo after submit),
  ShowDetails/ShowInfo (labeled + plain links, close hint),
  ShowWaiting/ShowProgress, Cancel (pending submit rejected with the
  reference error + onComplete(false)), HandleInput (escape/ctrl+c
  cancel, typing with backspace).
- Tests (+9, total 552): title + overrides, auth URL (hyperlink
  sequences + hint + instructions), device code, manual input flow
  (type/submit/echo), cancel (state + callback message + rejected
  submit), info links (labeled/plain/close), waiting + progress,
  completion, backspace. Race clean.

## CA8 ROUND-33 DONE (session selector search)

- codingagent/session_selector_search.go: components/session-selector-
  search.ts — ParseSearchQuery (re: regex mode with case-insensitive
  compile + empty/invalid errors, token mode with QUOTED PHRASES and
  the unclosed-quote fallback to plain whitespace tokenization),
  MatchSession (regex index*0.1 scoring, phrase indexOf over normalized
  lowercased text, fuzzy subsequence tokens — every token must match),
  FilterAndSortSessions (name-filter gating, recent keeps incoming
  order, relevance sorts score-asc with modified-desc tie-breaks,
  parse errors filter to empty).
- Tests (+14, total 566): parse (empty/tokens/phrases/unclosed-quote/
  regex valid-empty-invalid), match (empty query/fuzzy subsequence/
  all-tokens-required/phrase order-sensitive/regex match-miss-invalid),
  filter+sort (recent order, relevance near-first, name filter both
  modes, invalid regex empties), whitespace normalization. Race clean.

## CA8 ROUND-34 DONE (first-time setup component)

- codingagent/first_time_setup_component.go: components/first-time-
  setup.ts — FirstTimeSetupComponent with the two-step flow (theme ->
  analytics), logo + welcome block, detected-appearance line (unknown
  values fall back to the first option), option lists with the →
  cursor, theme preview callback on selection change (clamped at both
  ends, no duplicate previews), vim j/k navigation, enter advancing
  then submitting {theme, shareAnalytics}, escape/ctrl+c cancel, and
  the full-dialog rebuild so previews recolor every line.
- Tests (+10, total 576): initial step (logo/welcome/detection/cursor/
  skip hint), detected light, unknown detection fallback, preview
  (dark preview, top clamp, return to light), vim keys, advance to
  analytics (prompt/cursor/finish hint/settings.json explanation),
  submit declining, submit accepting, analytics clamping, cancel
  (escape + ctrl+c). Race clean.

## CA8 ROUND-35 DONE (oauth selector component)

- codingagent/oauth_selector_component.go: components/oauth-selector.ts
  — OAuthSelectorComponent with login/logout titles, auth-type labels
  ONLY when providers mix types (subscription vs API key wording),
  status indicators (nil -> "• unconfigured"; type mismatch ->
  subscription/API-key-configured warning; OAuth/stored credential ->
  "✓ configured"; env-style uppercase names prefixed "env:"; other
  sources verbatim), fuzzy search over name/id/authType/method with
  cursor clamping, 8-row window with scroll counter, three empty
  states (no providers login, none logged in logout, no matches),
  clamped (non-wrapping) navigation, enter selection carrying
  (id, authType), escape/ctrl+c cancel. The completion-list label
  stays in interactive_helpers.go; this file owns the selector wording.
- Tests (+10, total 586): selector label, login/logout titles, mixed
  vs single auth-type labels, five status indicators, search
  (filter + restore on backspace), initial search, clamped navigation,
  selection payload, cancel, three empty states. Race clean.

## CA8 ROUND-36 DONE (browser-launch containment + priority reset)

DEFECT: tests called OpenBrowser with literal example.com / auth.example.com URLs, so every suite run launched real browser tabs on the developer machine (TestOpenBrowser and the login-dialog ShowAuth path).

- codingagent/clipboard_browser.go: OpenBrowser now dispatches through
  the browserOpener seam (launchPlatformBrowser holds the platform
  exec); SetBrowserOpener swaps it and returns the restore func.
- codingagent/tests_main_test.go: TestMain installs a recording opener
  for the whole suite, so no test can ever reach the platform launcher;
  openedBrowserTargets/resetOpenedBrowserTargets expose the recording.
- clipboard_browser_test.go + login_dialog_component_test.go now assert
  dispatch targets through the recorder instead of launching.

PRIORITY RESET (user direction): core implementation first, peripheral UI polish deprioritized. Next rounds target the interactive-mode driver completion and the runtime LLM halves; the remaining selector widgets (tree/session/settings/config) stay queued behind them.

## CA8 ROUND-37 DONE (compaction summarization half — CORE)

The runtime LLM half of compaction lands: /compact and branch summaries now have the real summarization call instead of a seam.

- codingagent/compaction_summarization.go: core/compaction/
  compaction.ts summarization surfaces — SummarizationPrompt (the exact
  structured-checkpoint format), UpdateSummarizationInstructions +
  UpdateSummarizationPrompt (<previous-summary> merge), the retained
  TurnPrefixSummarizationPrompt, GetSummarizationFailure (error stop
  carries the provider message with an Unknown-error fallback; LENGTH
  stop is rejected because partial text must not become a session
  checkpoint), CreateSummarizationOptions (reasoning only for
  reasoning-capable models with a non-off level),
  SummarizationStreamFn (provider seam), CompleteSummarization (the
  shared choke point: cacheRetention none, caller routing preserved or a
  fresh session id for branch summaries, transport failures surfaced as
  error-stop messages so the shared retry policy applies, delegation to
  ai.RetryAssistantCall).
- TestMain browser recorder from the previous round keeps the suite from launching real browsers.
- Tests (+8, total 594): prompt structure (sections + preservation rule)
  across all three prompts, failure classifier (stop/toolUse persist;
  error with message and fallback; length rejected), option construction
  (reasoning pass-through, off/empty ignored, non-reasoning ignored),
  completion (success + cache/routing, fresh session id, transient retry
  to success, deterministic error NOT retried, nil stream fn rejected).

## CA8 ROUND-38 DONE (summary generation — CORE)

- codingagent/compaction_summarization.go: GenerateSummary +
  BuildSummarizationContext (compaction.ts) — standalone summary request
  (system prompt forbidding continuation + one user turn holding the
  <conversation> block), maxTokens = min(0.8 * reserveTokens, model
  max), initial vs update prompt selection with the "Additional focus"
  custom-instruction append, <previous-summary> wrapping, and the
  post-response gates: length-stop rejection, tool-call rejection,
  text-only extraction with usage passthrough.
- Tests (+6, total 600): context shape (system rules + conversation
  prompt), initial generation (text/usage/max-token budget/no previous
  tag), update + custom focus (previous-summary wrap + preserve rule),
  model token cap (4096 wins over 80000), length-stop rejection,
  tool-call rejection. Race clean.

## CA8 ROUND-39 DONE (branch summary generation — CORE)

- codingagent/compaction_summarization.go: GenerateBranchSummary
  (branch-summarization.ts) — token budget from the model context
  window minus reserve (16384 default), PrepareBranchEntries, typed
  materialization of raw branch payloads before ConvertToLlm (raw
  entries satisfied no ai.Message, so the serialized conversation
  silently emptied — tests caught it), the branch prompt with appended
  or REPLACED custom instructions, maxTokens min(4096, model max), the
  shared CompleteSummarization choke point, and the outcome gates:
  aborted -> {aborted}, length stop -> {error}, tool call -> {error},
  transport failure -> {error}; success prepends the exploration
  preamble, appends the read/modified file-operation lists, and returns
  usage.
- Tests (+6, total 606): empty entries, success (preamble + text +
  usage + branch prompt not the compaction one), custom instructions
  (appended vs replaced), aborted, three error paths (length/tool/
  transport), token budget (large window keeps everything, small window
  trims while keeping the tail). Race clean.

## CA8 ROUND-40 DONE (interactive event bridge — CORE)

The interactive driver now renders live agent activity instead of only dispatching input.

- codingagent/interactive_transcript.go: InteractiveTranscript — the
  agent-event bridge translating message_start/message_update/
  message_end into a streaming AssistantMessageComponent (finalized
  blocks keep their position; the live block renders as the tail),
  tool_execution_start/update/end into ToolExecutionComponents with
  streaming args, partial results, and the error flag, bash-block
  open/append for streamed shell output, render-order preservation,
  and Clear for session replacement.
- codingagent/tool_render_context.go: toolRenderersAdapter exposing a
  definition's RenderCall/RenderResult/RenderShell hooks as the
  transcript's ToolRenderers seam (call context marked started +
  args-complete; result context carries expanded/partial).
- ToolDefinition gained RenderCall/RenderResult hooks and
  ToolRenderContext gained the streaming fields (args, cwd, state,
  last component, invalidate, execution/args/partial/expanded/images/
  error flags) so custom tools can render.
- interactive_mode.go: the driver subscribes to the agent event stream
  on Run (disposer stored, unsubscribed on shutdown — registrations are
  effects), routes events into the transcript, and mounts the
  transcript in the chat stack above status/editor.
- Tests (+9, total 615): streaming assistant across start/update/end,
  multiple turns, tool execution with a text result, error results,
  custom renderers, bash blocks (+ unknown id ignored), render order
  (text before tool), clear, adapter battery (shell/call/result +
  hookless fallback). Race clean.

## CA8 ROUND-41 DONE (interactive run wiring — CORE)

The interactive driver now executes real agent runs, closing the input -> run -> render loop.

- codingagent/interactive_mode.go: sendPrompt persists the user message
  and dispatches one agent run OFF the input path (ContinueFrom on its
  own goroutine) so the driver keeps accepting steer/abort input while
  the transcript streams; runAgent guards against concurrent runs
  (queued status instead of a second run), clears the active flag at
  settle and requests a render, surfaces returned errors AND the
  agent's error-stop message (provider failures arrive as
  ErrorMessage, a returned error), plus IsRunActive/WaitForRun for
  deterministic tests.
- Tests (+4, total 619): prompt dispatch (run active, user message
  persisted, transcript shows the reply, Working status), queue-while-
  active (no concurrent run, Queued status, settle clears the flag),
  error surfacing (provider error banner), and the subscription
  disposer (events bridged during a run; after dispose the transcript
  stops growing). Race clean.

## CA8 ROUND-42 DONE (compaction orchestrator — CORE)

- codingagent/compaction_summarization.go: Compact + GenerateTurnPrefixSummary + CombineUsage (compaction.ts main path) — the split-turn merge (history summary + the "Turn Context (split turn)" separator + prefix checkpoint, usage combined across both calls), the history-only path, the previousSummary hoisting when no history messages remain, the migration error for a missing first-kept entry id, the turn-prefix budget (0.5 * reserve, model-capped) with its own checkpoint format, and the appended file-operation lists.
- compaction_utils.go: ComputeFileLists is nil-safe (a preparation
  without a tracker yields empty lists) — the orchestrator surfaced the
  nil dereference.
- Tests (+8, total 627): turn-prefix summary (budget/format/usage) and
  its length + tool-call rejections, CombineUsage (sums + optional
  fields only when reported), Compact guards (nil preparation,
  migration error), history-only (no split marker, usage, prompt
  count), split-turn merge (separator + combined usage), split without
  history (previous summary preserved), file-operation appending.
  Race clean.

## CA8 ROUND-43 DONE (compaction preparation — CORE)

- codingagent/compaction_summarization.go: PrepareCompaction + EstimateProjectedContextTokens + ShouldCompact + getCurrentSystemMessage + messagesFromProjectedEntryForCompaction (compaction.ts) — the cut-point plan over a session path (nil when the path already ends in a compaction or nothing falls before the cut), newest-compaction detection for the previousSummary restore and retained-tail boundary, projected context-token estimation that honors the latest provider usage unless a context edit or compaction invalidated it (falling back to system + message estimates), the threshold predicate, and the file-operation extraction across history and turn-prefix messages.
- compaction.go FIX: EstimateTokens is raw-payload aware — projected
  entries carry rawAgentMessage values, which estimated to 0 tokens and
  left every cut point at index 0 (nothing compressible). Decoding
  before estimating makes the cut-point accumulation real; the
  preparation tests caught it.
- Tests (+7, total 634): should-compact (threshold/disabled), path
  ending in compaction, cut planning over a 40-entry parent-linked path
  (first-kept id in path, history messages, tokens-before), previous
  summary honored through a mid-path compaction, nothing-to-do guard,
  projected estimation (fallback sums, nil projection). Race clean.

## CA8 ROUND-44 DONE (session compaction + /compact — CORE, wrap-up track)

SCOPE DECISION (user): minimal viable core, close out quickly. Remaining selector-class UI, the extensions loader, and experimental/ (CA9) are recorded as documented deviations instead of incomplete phases.

- codingagent/session_compaction.go: session-level compaction execution
  — CompactSession (prepare the current path, run the LLM
  summarization through the session's stream function, append the
  CompactionEntry with details/usage, emit compaction_end with the
  estimated post-checkpoint tokens, return the outcome; nothing
  compressible reports Executed=false), ShouldAutoCompact (context
  estimate vs the model window minus reserve), AutoCompactIfNeeded,
  SetSummarizationStreamFn (provider wiring seam), and the branch-path
  read.
- interactive_mode.go: /compact now executes a real compaction
  (unavailable without a summarization model, "Nothing to compact yet"
  when the cut keeps everything, outcome line with tokens + kept
  entry).
- Tests (+9, total 643): session compaction nothing-to-do / executes
  (summary + usage + persisted entry with the kept id) / failure
  propagation, should-auto-compact (small vs huge transcript),
  auto-compact no-op, /compact command (statuses Compacting ->
  Compacted), unavailable model, nothing-to-do. Race clean.

## CA8 ROUND-45 DONE (status/footer rendering + essential commands — CORE, wrap-up track)

- interactive_mode.go: status lines now RENDER into the status stack
  (severity-colored: error red, warning yellow, status plain; the
  newest status replaces the previous line as the reference's
  transient single-line slot) instead of only driving the test sink;
  the Footer mounts between the transcript and status (pwd + stats
  lines live in the layout).
- footer.go: Footer implements tui.Component (Render -> RenderLines);
  sessionFooterData adapts the session for git-branch + subscription
  lookups.
- Essential commands wired: /new (reset transcript + session name),
  /thinking (report/set with level validation), /model (current-model
  report; selector rides the seam), /export (JSONL to a path or the
  default name). /tree and remaining selector commands keep the seam
  message.
- Tests (+6, total 649): status rendering (plain/colored/replacement),
  footer lines in the layout, /new (transcript cleared), /thinking
  (report/set/invalid), /model report, /export (file written with the
  session content). Race clean.

## CA10 ROUND-2 DONE (final PARITY audit + acceptance — WRAP-UP)

- PARITY.md final state: CA2–CA8 ✅ (CA8 with the component-seam
  deviations), CA6 upgraded to ✅ with the full compaction chain; CA9
  recorded as the documented minimal-core deviation; header counts
  updated (175 files / 61.4K lines Go / 649 tests); the trailing gap
  note enumerates every unported block with line counts (17 selector
  components 6.2K, extensions loader 4.5K, experimental 10.7K,
  resource-loader/sdk/cache-warmer/migrations/rpc-client/startup-ui
  ~3.7K).
- docs/modules/codingagent.md + .zh.md: Modes section now covers the
  interactive driver; new Compaction section (full chain); Known
  limitations rewritten to the actual gap list.
- BINARY ACCEPTANCE (all four modes verified end-to-end on
  /tmp/openagent-final): --version (pi 0.0.0); --mode text (exit 0);
  --mode json (session header + agent_settled events); --mode rpc
  (get_state returns full session state, prompt returns disposition:
  started, invalid JSON returns the error envelope); --mode
  interactive (TTY gate: "interactive mode requires a TTY on stdin").
- Full gates: 30 packages ok, run-gates 7/7, codingagent -race clean,
  tui -race clean, linux/windows cross-builds pass.

## OCR REVIEW ROUND (opencodereview sweep — 24 fixes)

Ran the opencodereview CLI (`ocr review`) over the uncommitted work: 330 findings (14 critical / 63 high). Verified each serious claim against the code and fixed the confirmed ones:

CRITICAL fixes:
- compaction_summarization.go (x3): text-block extraction asserted *ai.TextContent while providers/decoders emit VALUE ai.TextContent — summaries silently emptied in production; extractTextBlocks now takes both forms. messages.go ConvertToLlm built pointer blocks that panic ai.ContentText's value assertion — now value blocks.
- tui/terminal.go: Rows() returned COLUMNS (terminalSize); now terminalRows.
- tui/editor_input.go jumpToChar: cursor-at-line-end searchFrom >
  len(line) slice panic — clamped.
- tui/autocomplete.go ApplyCompletion: cursor moved left of the
  recorded prefix → negative slice panic — clamped.
- tui/box_loader.go NewLoader: nil color fns dereferenced on render —
  identity defaults.
- interactive_transcript.go Clear: finalized map set to nil (assignment
  panic after /new + next message) — empty map.
- tool_definition.go toAgentToolResult: every non-AgentToolResult
  output silently emptied (all built-in tools starved the model) — converts the 7 known shapes (text/details/image) and errors on unknown types.
- print_mode.go sendPrompt: never ran the agent (text/json/rpc modes
  produced no model output) — drives ContinueFrom, error-stops surface uniformly. RPC test harness stubs the stream.
- utils_core.go: JPEG buffer[3] with a 3-byte file, BMP buffer[30:]
  below 34 bytes, empty frontmatter "---\n---" reverse slice — all bounded.

HIGH fixes: find_tool glob class `[/\]` -> `[/\\]` + fd exit-1 no-results path; model_runtime negative LastIndexByte slice -> filepath.Dir; settings_manager corrupted-project nil-map panic guarded; custom_components box rebuild duplication; model_catalog_refresh waiters race (increment under lock); clipboard RunClipboardCommand honors non-zero Wait exits (OSC 52 fallback no longer skipped); read_tool negative limit clamp; edit_diff panics -> error returns; session_files BMP guard. Also removed the 36MB stray binaries and gitignored them.

Regression tests added (codingagent/ocr_fixes_test.go, tui/ocr_fixes_test.go): value-typed blocks through all three summary paths, ConvertToLlm + ContentText, empty frontmatter, 3-byte JPEG, tool-result bridging (text/image/unknown), Rows/jumpToChar/ ApplyCompletion/loader-nil. All green: 30 packages, gates 7/7, codingagent + tui -race clean.

Remaining ocr findings NOT fixed this round (tracked for follow-up): SessionManager has no internal mutex (concurrent bash-result appends), tui scroll_view/Loader/autocomplete-debounce races, editor byte-vs-rune cursor units, latex parser overflow guards, Windows TTY probe via GetConsoleMode, SendPrompt ascii-only arg parsing, image EXIF orientation pipeline, get_tree shallow serialization, gen-unicode-tables double-escaping (needs regeneration + goldens), auth_storage parse-failure overwrite guard, minimatch globstar segment anchoring.

## SKILLS AUTODISCOVERY ROUND (package-manager resolve() skills slice)

- codingagent/package_manager.go: the skills half of addAutoDiscoveredResources — collectSkillEntries (SKILL.md-first walk; mode "pi" admits root markdown, mode "agents" only nested entries; per-dir ignore files via skills.go addIgnoreRules), findGitRepoRoot + collectAncestorAgentsSkillDirs (cwd upward through git repo root, else filesystem root), settings override matching (getOverridePatterns / normalizeExactPattern / matchesAnyPattern / matchesAnyExactPattern over the minimatch port; !glob exclude, +path force-include, -path force-exclude), isEnabledByOverrides, resourcePrecedenceRank (project local < project auto < user local < user auto < package), skillAccumulator (first record per path wins, stable rank sort, canonical dedupe). Resolve routes manifest + auto-discovered skills through the accumulator: project .pi/skills + ancestor .agents/skills (both trust-gated, home ~/.agents excluded from the project walk) then user agentDir/skills + ~/.agents/skills (unconditional; the cross-tool pool). resolveLocalEntries and extensions/prompts/themes auto-discovery remain with the resolve-completion round; resource-loader wiring (feeding resolved skills into sessions) still pending.
- Tests (+8, ported from package-manager.test.ts ".agents/skills auto-discovery"): git-root-bounded ancestor walk (above-repo excluded), walk to filesystem root without a repo, agents-mode markdown rules (root .md ignored; nested + deeply nested .md discovered), user-scoped ~/.agents/skills under overridden HOME (exactly once, scope user, source auto), symlink dedupe (agentDir/skills -> ~/.agents/skills), pi-mode root markdown discovery, parent .gitignore not applied to .pi auto-discovery; manifest test isolates HOME for hermetic exact counts.
- Docs: modules/codingagent(.zh).md configuration bullet + PARITY gap-list update.

## HARNESS RESOURCES ROUND (accept dispatch + facade Resources injection)

- agent/harness/runtime/request_dispatch.go: lane.ts accept()'s request-kind
  dispatch — RunRequest (prompt text/pre-built messages+images, skill,
  prompt_template), UnknownSkill/UnknownTemplate admission rejections
  (AcceptResult fields, mirroring Busy/Invalid), pending_assistant
  rejection over built messages, then AcceptRun with the config's queue
  modes. LaneOptions.ReadConfig injects the admission-time config
  snapshot (reference constructor param); nil fails loud. Skills resolve
  against Config.Resources (loaded values); loading stays with
  harness.LoadSkills/LoadSourcedSkills in the caller — no directory
  policy in the runtime.
- agent/harness/facade/facade.go: AgentHarnessOptions.Resources (TS
  resources?: Resources) flows into runtime.Config; queue-mode
  ("all") and tool-execution ("parallel") defaults resolved at this
  owning assembly point (harness.ts configStore seed); lanes built via
  ReadConfig. prompt.go adds Skill/PromptFromTemplate drive entries
  (driveRunRequest: dispatch-admit then drive to settlement).
- Tests (+10): dispatch (skill block expansion incl. location +
  additional instructions, template substitution "Fix A then A B",
  unknown skill/template rejections without installation, prompt message
  build, empty-prompt invalid, pending-assistant invalid, missing config
  source loud) + facade (Skill drives with expanded block on the
  transcript tip, unknown skill errors.As(UnknownSkill), template drive).
  gofmt/vet clean; ./agent/... 11 packages green.
- Design ruling recorded in conversation: loading at bare-agent (NewAgent)
  creation rejected — loop interface stays IO-free (reference agent.ts has
  zero skill knowledge); the configurable seam is Resources values at
  harness creation + app-side LoadSourcedSkills. Path/trust/override
  policy stays homed in codingagent.Resolve.

## COMPACTION EXTRACTION ROUND (agent/harness/compaction — openagent-native)

- agent/harness/compaction/: provider-agnostic compaction core extracted
  from the codingagent port (pi core/compaction/compaction.ts) for
  non-codingagent consumers; codingagent itself is untouched. Same
  provenance class as trajectory/ (openagent-native, not a 1:1 port);
  decision record in .agents/notes/implemented/architecture/
  2026-10-14-compaction-core-extraction.md.
- compaction.go: Settings/DefaultSettings + ShouldCompact threshold;
  PathEntry (linear branch-path entry contribution: ID, IsCompaction,
  Summary, Details, Messages); FindCutPoint backward token-budget walk
  with valid-cut snapping (user/assistant, never tool results), metadata
  pull-in, split-turn reporting; PrepareCompaction boundary handling over
  the newest contributing checkpoint (previous summary, post-boundary
  history, split-turn prefix, nil on nothing-compressible / trailing
  checkpoint).
- tokens.go: EstimateTokens (chars/4 per block), CalculateContextTokens,
  EstimateContextTokens (last valid assistant usage + trailing estimate;
  aborted/errored/zero-usage assistants ignored).
- summarization.go: reference prompts verbatim (initial/update/
  turn-prefix + system prompt), CompleteSummarization choke point
  (CacheRetention "none", fresh ai.UUIDv7 routing id, ai.RetryAssistantCall
  wrap), GenerateSummary/GenerateTurnPrefixSummary (0.8x / 0.5x reserve
  budgets, model cap, tool-call refusal, length-stop refusal),
  Compact (split-turn history + turn-prefix merge with separator,
  usage combination, file-list suffix). Transport credentials stay with
  the injected SummarizationStreamFn (reference ApiKey/Headers/Env
  plumbing dropped — documented deviation).
- utils.go: file-operation tracking (read/write/edit + path arg),
  ComputeFileLists/FormatFileOperations, SerializeConversation (labeled
  text, tool results truncated at 2000 chars), messagesToLlm passthrough.
- Tests (+16): threshold, per-role token estimation, usage-based context
  estimate incl. abort/error exclusion, cut-point budget snap/split/
  metadata adjustment, preparation boundaries (trailing checkpoint,
  previous-summary carry, single-entry nil), file ops, serialization
  truncation, summarization choke point (cache none, fresh routing id,
  transient retry, deterministic no-retry, nil stream fn), prompt shape
  (initial/update/custom focus/token cap), length/tool-call refusals,
  Compact plain + split turn, FramedSummary.
- Docs: modules/compaction(.zh).md + architecture map/extension-point
  entries (.md/.zh.md).

## TOOL LOADING ALIGNMENT ROUND (agent-session loadout wiring — CORE)

- codingagent/agent_session.go: SetActiveToolsByName now installs the
  executable loadout via Agent.SetTools (previously the session recorded
  names only, so every agent loop ran with zero tools: no declarations,
  no executions); buildRuntime registers the 8 builtin definitions in
  createAllToolDefinitions key order (read, bash, powershell, edit,
  write, grep, find, ls — powershell was ported but never registered)
  and wires settings into construction (read images.autoResize via
  GetImageAutoResize, bash shellCommandPrefix/shellPath); nil-vs-empty
  InitialActiveToolNames/AllowedToolNames semantics preserved (nil =
  default four + restoreToolsFromTranscript replay of the transcript's
  system-message toolsAdded/toolsRemoved; empty = explicit zero tools /
  empty registry); restoreToolsFromTranscript ports the pinned
  _restoreToolsFromTranscript via BuildSessionProjectionFromLeaf.
- codingagent/settings_manager.go: DefaultToolNames +
  ResolveDefaultTools (+name/-name modifier resolution, plain names
  replace the default; nil-vs-empty: unset setting returns nil) +
  GetImageAutoResize (handles Settings-typed nested maps, which do not
  assert to map[string]any).
- codingagent/cli_args.go + cmd/openagent/main.go: PrintSessionOptions
  carries Tools/ExcludeTools/NoTools; applyPrintToolSelection ports the
  sdk.ts chain (tools ?? noTools ? [] : defaultTools ?? DEFAULT, minus
  excludes; allowlist = tools ?? (noTools=="all" ? [] : nil)); main.ts
  flag mapping via noToolsModeFromCli (--no-tools -> all,
  --no-builtin-tools -> builtin); --tools/--exclude-tools parsing keeps
  flag presence distinct from an all-empty value (pi assigns []).
- Tests: agent_session (installed-on-agent default + filtered, explicit
  empty selection, empty allowlist, transcript restore, explicit initial
  skips restore), cli_args (presence semantics + applyPrintToolSelection
  battery), settings (ResolveDefaultTools 8-case battery, unset vs empty
  defaultTools, images.autoResize round-trip).
- Docs: modules/codingagent(.zh).md tools loading paragraph + PARITY CA6
  row update.

## TOOLSET ADAPTER ROUND (agent/harness/toolset — openagent-native)

- agent/harness/toolset/toolset.go: two-tier seam for loading harness
  tools onto the bare agent loop — Adapt (pure pipeline wrapper:
  Prepare -> Before -> Execute -> Finalize, abort->chord weaving, update
  callback mapping, IsError -> error text) and Local (env+gate bundle,
  idempotent Close: gate -> CleanupShell -> Cleanup ->
  ReleaseMutationQueue, errors joined) + Coding() four; admission panics
  (AbortRequested, gate closed) convert to error returns at the seam —
  the loop's Execute contract; escaping them would crash the loop (no
  recover there). Seam placement decision + alternatives in Agent Note
  toolset-adapter-seam (.md/.zh.md).
- agent/harness/tools/builtin.go: exported ReleaseMutationQueue(env) —
  Go-native cleanup for the WeakMap-replacement global queue map
  (reference collects per-env state with GC; the port leaked it).
- examples/coding_agent: adaptHarnessTool/adaptUpdateCallback removed;
  bundle usage is now toolset.NewLocal(dir, toolset.Coding()) +
  ts.Close(); tests drive toolset.Adapt.
- Tests (+7): Adapt write/read round trip, edit replace, bash
  success/exit-7 error text, invalid args fail loud, update callback
  streams, nil env/gate panics at assembly, Local close lifecycle
  (idempotent, post-close execute reports "gate closed"), mutation
  queue release allows requeue.
- Docs: modules/toolset(.zh).md new pair; PARITY harness tools/toolset
  rows; Agent Note pair.

## LOCAL ENV SPILL ROUND (env/local spill-to-temp-file)

- agent/harness/types.go: ExecErrSpillError = "spill_error" joins the
  closed error-code set.
- agent/harness/env/local/local.go: execCapture grows the pi spill path —
  chunks buffer (copied; the read loop reuses its buffer) until the view
  first truncates, then the buffered prefix plus every later chunk append
  to CreateTempFile("pi-output-", ".log") so the file holds the complete
  output; the path publishes via capture.SetSpillPath; writes are
  synchronous (bounded pipe = inherent backpressure; the TS
  SPILL_HIGH_WATER_MARK stream mark has no Go counterpart, placeholder
  const removed); a failed create/write records spill_error and fails the
  exec after the child settles (mirrors nodejs.ts settle(spillError));
  finish closes the file before the final snapshot.
- Tests: env/local (+4) — line-limit trigger (spill = full 6000 lines,
  view = tail), bytes trigger (single 100KB line; spill 100000B), no
  spill under limit, spill disabled stays empty; toolset (+1) — adapted
  bash footnote carries the real spill path and the file materializes.
- Docs: PARITY env/local row replaces the spill deferral note.
