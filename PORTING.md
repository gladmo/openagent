# openagent: Go 1:1 rewrite of pi/packages/agent

Reference: `../pi` at commit `ff72faba2` (packages: agent v0.87.1 + ai/chord/telemetry closures).Approved plan: mirror modules/names/behavior; dependency closures as separate Go packages; tests ported core-first (e2e + benchmarks excluded).

## Status

| Phase | Scope | Status |
|---|---|---|
| P0 | base libs: abort, jsonx, typebox, partialjson, diff, ignore | **done** |
| P1 | telemetry (full) + tests | **done** |
| P2 | chord closure (root json/types, context, delta/index) + tests | **done** |
| P3 | ai closure (types, models, faux, utils subset, auth subset) + tests | **done** |
| P4 | agent root pkg (types, stream-fn, agent-loop, agent, proxy, search) + tests | **in progress** |
| P5 | harness top-level + utils + tools + env/nodejs + tests | pending |
| P6 | session core + jsonl + session/testing + tests | pending |
| P7 | runtime (lane, drive pipeline, reducer, restore) + agent-harness + tests | pending |
| P8 | chord tracker/services subset + pico3 + prioritized tests | pending |
| P9 | PARITY.md export audit + full suite + JSONL interop spot check | pending |

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
              nil onUpdate guarded); agent/harness/env/nodejs/ (real
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
              (mirrors nodejs env WriteFile); MEMORY: JsonlSessionRepo does
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
