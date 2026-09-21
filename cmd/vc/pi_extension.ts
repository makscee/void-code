// void-code-managed-pi-extension:v1
import { execFileSync, spawn as nativeSpawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { isDeepStrictEqual } from "node:util";
import { existsSync, renameSync, writeFileSync } from "node:fs";
import { getPackageDir, VERSION } from "@earendil-works/pi-coding-agent";
import path from "node:path";
import { pathToFileURL } from "node:url";
import type {
	AssistantMessage,
	AssistantMessageEventStream,
	Context,
	Model,
	SimpleStreamOptions,
} from "@earendil-works/pi-ai";
import { clampThinkingLevel, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { isKeyRelease, matchesKey } from "@earendil-works/pi-tui";

const CODEX_PROVIDER_ID = "void-codex";
const CODEX_MODEL_ID = "gpt-5.6-terra";

interface BootstrapProvider {
	kind: "codex";
	relayProviderId: string;
	models: string[];
}
interface Bootstrap {
	version: number;
	relayUrl: string;
	authToken: string;
	providers: BootstrapProvider[];
	modelDecision?: ModelDecisionConfig;
}
const MANAGED_WEB_SEARCH_INSTRUCTION = "For current or externally verifiable facts, use web_search. Use multiple queries for research, inspect primary sources with fetch_content, and cite links. Use get_search_content to revisit stored results.";

interface ClipboardIOOptions {
	platform: string;
	env: Record<string, string | undefined>;
	piVersion: string;
	writeText: (text: string, signal?: AbortSignal) => Promise<void>;
}

interface FullscreenClipboardOptions extends ClipboardIOOptions {
	notify: (message: string, level: "info" | "warning" | "error") => void;
}

interface NativeClipboardWriterOptions {
	platform: string;
	env: Record<string, string | undefined>;
	spawn?: typeof nativeSpawn;
}

interface ClipboardExtensionOptions {
	clipboardIO?: ClipboardIOOptions;
	modelDecision?: ModelDecisionOptions;
}


export default function (pi: ExtensionAPI, options?: ClipboardExtensionOptions) {
	registerDesktopLifecycle(pi);
	registerFullscreenClipboardLifecycle(pi, options?.clipboardIO);
	const bootstrap = options?.modelDecision ? options.modelDecision.bootstrap : loadBootstrap();
	if (!bootstrap) return;
	const parsed = parseBootstrap(bootstrap);
	if (!parsed.ok) return;
	const transport = parsed.bootstrap;
	const allowed = new Set([CODEX_MODEL_ID, "gpt-5.6-sol", "gpt-5.6-luna", "gpt-6-astra"]);
	const ceiling = options?.modelDecision?.compatibilityModels ?? transport.providers.flatMap(provider => {
		if (provider.kind === "codex") {
			return provider.models.filter(id => allowed.has(id)).map(id => codexModel(id, codexName(id)));
		}
		return [];
	});
	const controller = new ModelDecisionController(pi, transport, ceiling, options?.modelDecision);
	modelControllers.set(pi, controller);
	let managedSearchAvailable = false;
	for (const provider of transport.providers) {
		if (provider.kind === "codex") managedSearchAvailable = true;
	}
	if (managedSearchAvailable) pi.on("before_agent_start", async event => ({ systemPrompt: event.systemPrompt + "\n\n" + MANAGED_WEB_SEARCH_INSTRUCTION }));
}

function registerVoidCodex(
	pi: ExtensionAPI,
	bootstrap: Bootstrap,
	models: Model<any>[],
	relayProviderId: string | undefined,
	controller: ModelDecisionController,
): void {
	const stream = bindVoidCodexStream(bootstrap, controller);
	pi.registerProvider(CODEX_PROVIDER_ID, {
		name: "Void ChatGPT relay",
		baseUrl: bootstrap.relayUrl,
		apiKey: bootstrap.authToken,
		api: "void-codex-sse",
		...(relayProviderId ? { headers: { "x-void-provider": relayProviderId } } : {}),
		models,
		streamSimple: stream,
	});
}

// Pi does not expose fullscreen selection text to extensions. This adapter is deliberately
// version-bound to 0.84.1 and delegates extraction to TuiAltScreen.copySelectionToClipboard on a
// synchronous disposable receiver and terminal sink. The live ProcessTerminal and flash function
// are never replaced. Shape guards make a future Pi change fail passive instead of taking authority.
interface FullscreenClipboardOwner {
	target: object;
	dispose: () => void;
}
const fullscreenClipboardOwners = new WeakMap<object, FullscreenClipboardOwner>();
const fullscreenClipboardReferences = new WeakMap<object, FullscreenClipboardOwner>();
const CLIPBOARD_WIDGET_KEY = "void-code-fullscreen-clipboard";
const MAX_CLIPBOARD_BYTES = 8 * 1024 * 1024;
const MAX_CLIPBOARD_WAITING = 8;

function clipboardPayloadAllowed(text: string): boolean {
	return !text.includes("\0") && Buffer.byteLength(text, "utf8") <= MAX_CLIPBOARD_BYTES;
}

function clipboardCopyIntent(data: string, platform: string): boolean {
	return matchesKey(data, "ctrl+c") || (platform === "darwin" && matchesKey(data, "super+c"));
}

function clipboardSelectionMouseInput(data: string): boolean {
	const sgr = /^\x1b\[<(\d+);\d+;\d+[Mm]$/.exec(data);
	if (sgr) {
		const button = Number.parseInt(sgr[1], 10);
		return (button & 64) === 0 && (button & 3) === 0;
	}
	if (data.length === 6 && data.startsWith("\x1b[M")) {
		const button = data.charCodeAt(3) - 32;
		return (button & 64) === 0 && ((button & 3) === 0 || (button & 3) === 3);
	}
	return false;
}

function clipboardAuthority(platform: string, env: Record<string, string | undefined>): boolean {
	if (platform !== "darwin" && platform !== "win32") return false;
	const executable = env.VC_BOOTSTRAP_EXECUTABLE;
	if (!executable || !path.isAbsolute(executable)) return false;
	if ((env.SSH_CONNECTION || env.SSH_CLIENT || env.SSH_TTY) && env.VC_DESKTOP_SESSION !== "1" && !env.VC_DESKTOP_CHAT_ID) return false;
	return true;
}

function resolveFullscreenClipboardTarget(reference: any): object | undefined {
	if (!reference || (typeof reference !== "object" && typeof reference !== "function")) return undefined;
	const key = Symbol();
	const reveal = function (this: object): object { return this; };
	let target: object | undefined;
	try {
		if (!Reflect.set(reference, key, reveal, reference)) return undefined;
		const method = Reflect.get(reference, key, reference);
		if (typeof method !== "function") return undefined;
		const candidate = Reflect.apply(method, reference, []);
		if (!candidate || (typeof candidate !== "object" && typeof candidate !== "function")) return undefined;
		target = candidate;
		const descriptor = Object.getOwnPropertyDescriptor(target, key);
		if (!descriptor?.configurable || descriptor.value !== reveal) return undefined;
	} catch {
		return undefined;
	} finally {
		try {
			if (target && Object.getOwnPropertyDescriptor(target, key)?.value === reveal && !Reflect.deleteProperty(target, key)) target = undefined;
		} catch {
			target = undefined;
		}
		if (reference !== target) {
			try {
				if (Object.getOwnPropertyDescriptor(reference, key)?.value === reveal && !Reflect.deleteProperty(reference, key)) target = undefined;
			} catch {
				target = undefined;
			}
		}
	}
	try {
		return target && !Object.prototype.hasOwnProperty.call(target, key) ? target : undefined;
	} catch {
		return undefined;
	}
}

export function installFullscreenClipboard(tui: any, options: FullscreenClipboardOptions): () => void {
	if (!tui || (typeof tui !== "object" && typeof tui !== "function")) return () => {};
	const reference = tui as object;
	const previous = fullscreenClipboardReferences.get(reference);
	const authority = clipboardAuthority(options.platform, options.env);
	if (!authority || options.piVersion !== "0.84.1") {
		const owner = previous ?? fullscreenClipboardOwners.get(reference);
		owner?.dispose();
		if (previous && fullscreenClipboardReferences.get(reference) === previous) fullscreenClipboardReferences.delete(reference);
		if (authority) {
			options.notify("Fullscreen native clipboard is unavailable for this Pi runtime.", "warning");
		}
		return () => {};
	}
	const failPassive = (): (() => void) => {
		options.notify("Fullscreen native clipboard is unavailable for this Pi runtime.", "warning");
		return () => {};
	};
	const target = resolveFullscreenClipboardTarget(tui);
	if (previous && (previous.target !== target || fullscreenClipboardOwners.get(previous.target) !== previous)) {
		previous.dispose();
		if (fullscreenClipboardReferences.get(reference) === previous) fullscreenClipboardReferences.delete(reference);
	}
	if (!target) return failPassive();
	tui = target;
	if (tui.mode === "regular") {
		previous?.dispose();
		return () => {};
	}
	if (typeof tui.copySelectionToClipboard !== "function" || typeof tui.getSelectionBounds !== "function" ||
		typeof tui.handleSelectionMouseEvent !== "function" || typeof tui.handleViewportInput !== "function" ||
		typeof tui.setFocus !== "function" || typeof tui.showOverlay !== "function" || typeof tui.addInputListener !== "function" ||
		typeof tui.flash !== "function" || !tui.terminal || typeof tui.terminal.write !== "function") {
		previous?.dispose();
		return failPassive();
	}
	const existing = fullscreenClipboardOwners.get(target);
	if (existing) {
		fullscreenClipboardReferences.set(reference, existing);
		return () => {};
	}

	const originalCopy = tui.copySelectionToClipboard;
	const originalSelectionMouse = tui.handleSelectionMouseEvent;
	const originalViewportInput = tui.handleViewportInput;
	const originalSetFocus = tui.setFocus;
	const originalShowOverlay = tui.showOverlay;
	const inheritedHooks: Array<[string, any]> = [
		["copySelectionToClipboard", originalCopy], ["handleSelectionMouseEvent", originalSelectionMouse],
		["handleViewportInput", originalViewportInput], ["setFocus", originalSetFocus], ["showOverlay", originalShowOverlay],
	].filter(([key]) => !Object.prototype.hasOwnProperty.call(tui, key));
	let disposed = false;
	let active: AbortController | undefined;
	let selectionFresh = false;
	let selectionFocus: unknown;
	const waiting: Array<{ text: string }> = [];
	let dispose = (): void => {};
	const retainOwnership = (): boolean => {
		if (disposed) return false;
		if (resolveFullscreenClipboardTarget(reference) === target) return true;
		dispose();
		return false;
	};

	const runNext = (): void => {
		if (!retainOwnership() || active || waiting.length === 0) return;
		const item = waiting.shift()!;
		const controller = new AbortController();
		active = controller;
		Promise.resolve()
			.then(() => options.writeText(item.text, controller.signal))
			.then(() => { if (retainOwnership() && !controller.signal.aborted) tui.flash("Copied!"); })
			.catch(() => { if (retainOwnership() && !controller.signal.aborted) options.notify("Clipboard copy failed.", "error"); })
			.finally(() => {
				if (active === controller) active = undefined;
				if (!disposed) runNext();
			});
	};
	const admit = (text: string): void => {
		if (text.length === 0) return;
		if (!clipboardPayloadAllowed(text)) {
			options.notify("Clipboard selection is too large or unsupported.", "warning");
			return;
		}
		if (waiting.length >= MAX_CLIPBOARD_WAITING) {
			options.notify("Clipboard copy queue is full.", "warning");
			return;
		}
		waiting.push({ text });
		runNext();
	};

	const managedCopy = function (this: any): void {
		if (!retainOwnership()) {
			originalCopy.call(this);
			return;
		}
		if (!tui.getSelectionBounds()) return;
		const terminal = tui.terminal;
		const originalWrite = terminal.write;
		const originalFlash = tui.flash;
		let extracted: string | undefined;
		const terminalSink = Object.create(terminal);
		Object.defineProperty(terminalSink, "write", { configurable: true, value: (output: string): void => {
			const match = typeof output === "string" ? /^\x1b\]52;c;([^\x07]*)\x07$/.exec(output) : null;
			if (match) {
				try { extracted = Buffer.from(match[1], "base64").toString("utf8"); } catch {}
				return;
			}
			originalWrite.call(terminal, output);
		} });
		const receiver = Object.create(tui);
		Object.defineProperties(receiver, {
			terminal: { configurable: true, value: terminalSink },
			flash: { configurable: true, value: (message: string, ...args: any[]): any =>
				message === "Copied!" ? undefined : originalFlash.call(tui, message, ...args) },
		});
		originalCopy.call(receiver);
		if (extracted !== undefined) admit(extracted);
	};
	tui.copySelectionToClipboard = managedCopy;
	const managedSelectionMouse = function (this: any, event: any): any {
		if (!retainOwnership()) return originalSelectionMouse.call(this, event);
		if (!event?.release && (event?.button & 35) === 0) {
			selectionFresh = true;
			selectionFocus = tui.focusedComponent;
		}
		// Pi normally copies from the mouse handler on release/double/triple click. Keep all
		// native selection geometry while withholding clipboard authority until a copy key.
		const copyAtInput = tui.copySelectionToClipboard;
		const silentCopy = (): void => {};
		tui.copySelectionToClipboard = silentCopy;
		let result: any;
		try { result = originalSelectionMouse.call(this, event); }
		finally { if (tui.copySelectionToClipboard === silentCopy) tui.copySelectionToClipboard = copyAtInput; }
		if (event?.release && !tui.getSelectionBounds()) selectionFresh = false;
		return result;
	};
	const managedViewportInput = function (this: any, data: string): any {
		if (!retainOwnership()) return originalViewportInput.call(this, data);
		// Pi's viewport listener predates extension listeners and can consume navigation before
		// they observe it. Retire selection authority here while preserving both copy intents.
		if (!isKeyRelease(data) && !clipboardSelectionMouseInput(data) && !clipboardCopyIntent(data, options.platform)) selectionFresh = false;
		return originalViewportInput.call(this, data);
	};
	const managedSetFocus = function (this: any, component: any): any {
		if (!retainOwnership()) return originalSetFocus.call(this, component);
		if (selectionFresh && component !== selectionFocus) selectionFresh = false;
		return originalSetFocus.call(this, component);
	};
	const managedShowOverlay = function (this: any, ...args: any[]): any {
		if (!retainOwnership()) return originalShowOverlay.apply(this, args);
		selectionFresh = false;
		return originalShowOverlay.apply(this, args);
	};
	tui.handleSelectionMouseEvent = managedSelectionMouse;
	tui.handleViewportInput = managedViewportInput;
	tui.setFocus = managedSetFocus;
	tui.showOverlay = managedShowOverlay;

	const removeInputListener = tui.addInputListener((data: string) => {
		if (!retainOwnership() || isKeyRelease(data)) return;
		const interruptCopy = matchesKey(data, "ctrl+c");
		const copyOnly = options.platform === "darwin" && matchesKey(data, "super+c");
		if (!interruptCopy && !copyOnly) {
			selectionFresh = false;
			return;
		}
		if ((typeof tui.hasOverlay === "function" && tui.hasOverlay()) || tui.focusedComponent !== selectionFocus) {
			selectionFresh = false;
			return copyOnly ? { consume: true } : undefined;
		}
		const selection = tui.getSelectionBounds();
		if (!selectionFresh || !selection) {
			selectionFresh = false;
			return copyOnly ? { consume: true } : undefined;
		}
		managedCopy.call(tui);
		return { consume: true };
	});

	dispose = (): void => {
		if (disposed) return;
		disposed = true;
		waiting.length = 0;
		active?.abort();
		removeInputListener();
		if (tui.copySelectionToClipboard === managedCopy) tui.copySelectionToClipboard = originalCopy;
		if (tui.handleSelectionMouseEvent === managedSelectionMouse) tui.handleSelectionMouseEvent = originalSelectionMouse;
		if (tui.handleViewportInput === managedViewportInput) tui.handleViewportInput = originalViewportInput;
		if (tui.setFocus === managedSetFocus) tui.setFocus = originalSetFocus;
		if (tui.showOverlay === managedShowOverlay) tui.showOverlay = originalShowOverlay;
		for (const [key, original] of inheritedHooks) {
			if (tui[key] === original) Reflect.deleteProperty(tui, key);
		}
		const owner = fullscreenClipboardOwners.get(target);
		if (owner?.dispose === dispose) fullscreenClipboardOwners.delete(target);
		if (owner && fullscreenClipboardReferences.get(reference) === owner) fullscreenClipboardReferences.delete(reference);
	};
	const owner = { target, dispose };
	fullscreenClipboardOwners.set(target, owner);
	fullscreenClipboardReferences.set(reference, owner);
	return dispose;
}

export function createNativeClipboardWriter(options: NativeClipboardWriterOptions): (text: string, signal?: AbortSignal) => Promise<void> {
	const spawn = options.spawn ?? nativeSpawn;
	let active = false;
	const waiting: Array<{ text: string; signal?: AbortSignal; resolve: () => void; reject: (error: Error) => void }> = [];
	const genericError = () => new Error("Native clipboard write failed.");

	let file: string;
	let args: string[];
	if (options.platform === "darwin") {
		file = "/usr/bin/osascript";
		args = ["-l", "JavaScript", "-e", "ObjC.import('Foundation'); ObjC.import('AppKit'); const data = $.NSFileHandle.fileHandleWithStandardInput.readDataToEndOfFile; const text = $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding); if (!text) throw new Error('Native clipboard write failed.'); const pasteboard = $.NSPasteboard.generalPasteboard; pasteboard.clearContents; if (!pasteboard.setStringForType(text, $.NSPasteboardTypeString)) throw new Error('Native clipboard write failed.');"];
	} else if (options.platform === "win32") {
		const root = options.env.SystemRoot || options.env.WINDIR || "C:\\Windows";
		file = path.win32.join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
		args = ["-NoProfile", "-NonInteractive", "-Sta", "-Command", "$ErrorActionPreference='Stop'; $stream=[Console]::OpenStandardInput(); $utf8=[Text.UTF8Encoding]::new($false,$true); $reader=[IO.StreamReader]::new($stream,$utf8,$false); try { $text=$reader.ReadToEnd() } finally { $reader.Dispose() }; [void][Reflection.Assembly]::Load('System.Windows.Forms, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089'); [Windows.Forms.Clipboard]::SetText($text)"];
	} else {
		return async () => { throw genericError(); };
	}
	if (!path.isAbsolute(file) && !path.win32.isAbsolute(file)) return async () => { throw genericError(); };
	const spawnOptions = { shell: false, stdio: ["pipe", "ignore", "pipe"] as ["pipe", "ignore", "pipe"], windowsHide: true };

	const pump = (): void => {
		if (active) return;
		while (waiting[0]?.signal?.aborted) waiting.shift()!.reject(genericError());
		if (waiting.length === 0) return;
		active = true;
		const item = waiting.shift()!;
		let child: any;
		try {
			child = spawn(file, args, spawnOptions);
		} catch {
			active = false;
			item.reject(genericError());
			pump();
			return;
		}
		child.stderr?.resume?.();
		let failed = false;
		let closed = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const abort = (): void => {
			failed = true;
			// Completion remains pending until close: the child is terminated, then reaped, before pump.
			try { child.kill("SIGKILL"); } catch {}
		};
		const finish = (code: number | null): void => {
			if (closed) return;
			closed = true;
			if (timer) clearTimeout(timer);
			item.signal?.removeEventListener("abort", abort);
			active = false;
			if (!failed && code === 0) item.resolve(); else item.reject(genericError());
			pump();
		};
		child.once("error", () => { failed = true; try { child.kill(); } catch {} });
		child.stdin.once("error", () => { failed = true; try { child.kill(); } catch {} });
		child.once("exit", (code: number | null) => { if (code !== 0) failed = true; });
		child.once("close", (code: number | null) => finish(code));
		item.signal?.addEventListener("abort", abort, { once: true });
		timer = setTimeout(abort, 5000);
		try {
			child.stdin.setDefaultEncoding?.("utf8");
			child.stdin.end(item.text, "utf8");
		} catch {
			failed = true;
			try { child.kill(); } catch {}
		}
	};

	return (text: string, signal?: AbortSignal): Promise<void> => {
		if (signal?.aborted || !clipboardPayloadAllowed(text)) return Promise.reject(genericError());
		if (waiting.length >= MAX_CLIPBOARD_WAITING) return Promise.reject(genericError());
		return new Promise<void>((resolve, reject) => {
			waiting.push({ text, signal, resolve, reject });
			pump();
		});
	};
}

function registerFullscreenClipboardLifecycle(pi: ExtensionAPI, injected?: ClipboardIOOptions): void {
	let dispose: (() => void) | undefined;
	pi.on("session_start", async (_event, ctx) => {
		dispose?.();
		dispose = undefined;
		if (ctx.mode !== "tui" || !ctx.hasUI) return;
		const io = injected ?? {
			platform: process.platform,
			env: process.env,
			piVersion: VERSION,
			writeText: createNativeClipboardWriter({ platform: process.platform, env: process.env }),
		};
		ctx.ui.setWidget(CLIPBOARD_WIDGET_KEY, (tui) => {
			const options = {
				...io,
				notify: (message: string, level: "info" | "warning" | "error") => ctx.ui.notify(message, level),
			};
			let currentTarget: object | undefined;
			let currentDispose = (): void => {};
			let disposed = false;
			const refresh = (): void => {
				if (disposed) return;
				const target = resolveFullscreenClipboardTarget(tui);
				if (target === currentTarget) return;
				currentDispose();
				currentTarget = target;
				currentDispose = installFullscreenClipboard(tui, options);
			};
			refresh();
			const installedDispose = (): void => {
				if (disposed) return;
				disposed = true;
				currentDispose();
				currentTarget = undefined;
			};
			dispose = installedDispose;
			return { render: () => { refresh(); return []; }, invalidate: () => {}, dispose: installedDispose };
		});
	});
	pi.on("session_shutdown", async () => {
		dispose?.();
		dispose = undefined;
	});
}

function registerDesktopLifecycle(pi: ExtensionAPI): void {
	const statusPath = process.env.VC_DESKTOP_STATUS_PATH;
	const chatId = process.env.VC_DESKTOP_CHAT_ID;
	const generation = Number(process.env.VC_DESKTOP_STATUS_GENERATION);
	if (!statusPath && !chatId && !process.env.VC_DESKTOP_STATUS_GENERATION) return;
	if (!statusPath || !path.isAbsolute(statusPath) || !chatId || !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(chatId) || !Number.isSafeInteger(generation) || generation < 1) {
		console.error("void-code: desktop lifecycle channel unavailable");
		return;
	}
	let sequence = 0;
	const emit = (state: "Working" | "Ready"): void => {
		sequence += 1;
		const message = { version: 1, chatId, generation, sequence, state, timestamp: new Date().toISOString() };
		const temporary = statusPath + ".tmp-" + process.pid;
		try {
			writeFileSync(temporary, JSON.stringify(message) + "\n", { mode: 0o600 });
			renameSync(temporary, statusPath);
		} catch {
			console.error("void-code: desktop lifecycle channel unavailable");
		}
	};
	pi.on("before_agent_start", async () => { emit("Working"); });
	pi.on("agent_end", async () => { emit("Ready"); });
}

function loadBootstrap(): Bootstrap | undefined {
	try {
		const executable = process.env.VC_BOOTSTRAP_EXECUTABLE;
		if (!executable || !path.isAbsolute(executable)) throw new Error("trusted vc bootstrap executable unavailable");
		const raw = execFileSync(executable, ["pi-bootstrap"], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 15000 });
		const value = JSON.parse(raw) as Bootstrap;
		if ((value.version !== 1 && value.version !== 2) || !value.relayUrl || !value.authToken || !Array.isArray(value.providers)) throw new Error("invalid bootstrap response");
		return value;
	} catch (error) {
		console.error("void-code: managed Pi provider unavailable; run vc login, then vc (" + (error instanceof Error ? error.message : String(error)) + ")");
		return undefined;
	}
}

function codexName(id: string): string {
	if (id === "gpt-5.6-sol") return "GPT-5.6 Sol via Void relay";
	if (id === "gpt-5.6-luna") return "GPT-5.6 Luna via Void relay";
	if (id === "gpt-6-astra") return "GPT-6 Astra via Void relay";
	return "GPT-5.6 Terra via Void relay";
}

function codexModel(id: string, name: string): Model<any> {
	return {
		id,
		name,
		reasoning: true,
		thinkingLevelMap: { xhigh: "xhigh", minimal: "low" },
		input: ["text", "image"],
		cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		// This private ChatGPT backend rejects around 385k tokens despite the
		// public API's 1.05M claim. Keep Pi's effective window conservative so
		// its default 16,384-token reserve compacts at 255,616 tokens.
		contextWindow: 272000,
		maxTokens: 128000,
	};
}

function bindVoidCodexStream(
	bootstrap: Bootstrap,
	controller: ModelDecisionController,
): (model: Model<any>, context: Context, options?: SimpleStreamOptions) => AssistantMessageEventStream {
	return (model, context, options) => streamVoidCodexWithAuthority(model, context, options, bootstrap, controller);
}

export function streamVoidCodex(
	model: Model<any>,
	context: Context,
	options?: SimpleStreamOptions,
): AssistantMessageEventStream {
	return streamVoidCodexWithAuthority(model, context, options);
}

function streamVoidCodexWithAuthority(
	model: Model<any>,
	context: Context,
	options?: SimpleStreamOptions,
	bootstrap?: Bootstrap,
	controller?: ModelDecisionController,
): AssistantMessageEventStream {
	const stream = createAssistantMessageEventStream();

	(async () => {
		const output: AssistantMessage = {
			role: "assistant",
			content: [],
			api: model.api,
			provider: model.provider,
			model: model.id,
			usage: {
				input: 0,
				output: 0,
				cacheRead: 0,
				cacheWrite: 0,
				totalTokens: 0,
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
			},
			stopReason: "stop",
			timestamp: Date.now(),
		};

		try {
			if (!controller || !controller.isSelectable(model.provider, model.id)) throw new Error("Void model decision is closed");
			if (!bootstrap) throw new Error("Void provider bootstrap is unavailable");
			const relayURL = bootstrap.relayUrl.replace(/\/+$/, "") + "/codex/responses";
			const token = bootstrap.authToken;
			const providerID = bootstrap.providers.find((provider) => provider.kind === "codex")?.relayProviderId;
			if (!providerID) throw new Error("Void Codex provider grant is unavailable");
			let requestBody = await buildCodexBody(model, context, options);
			const nextBody = await options?.onPayload?.(requestBody, model);
			if (nextBody !== undefined) requestBody = nextBody as Record<string, unknown>;
			requestBody = await sanitizeCodexBody(requestBody);
			const body = JSON.stringify(requestBody);
			const headers: Record<string, string> = {
				"accept": "text/event-stream",
				"content-type": "application/json",
				"authorization": "Bearer " + token,
				"x-void-provider": providerID,
			};
			if (options?.sessionId) headers["x-pi-session-id"] = options.sessionId;

			// This is the admission linearization point, after all asynchronous payload hooks.
			if (!controller.isSelectable(model.provider, model.id)) throw new Error("Void model decision is closed");
			const request = controller.requestContext();
			const response = await fetch(relayURL, {
				method: "POST",
				headers,
				body,
				signal: options?.signal,
			});
			const snapshot = snapshotDecisionHeaders(response.headers);
			await controller.receiveHeaders(snapshot, request);
			await options?.onResponse?.({ status: response.status, headers: headersToRecord(response.headers) }, model);
			if (!response.ok) {
				throw new Error("Void relay Codex request failed: HTTP " + response.status + ": " + (await response.text()));
			}
			if (!response.body) throw new Error("Void relay Codex response had no body");

			stream.push({ type: "start", partial: output });
			const { processResponsesStream } = await openAIResponsesShared();
			await processResponsesStream(parseSSE(response, options?.signal), output, stream, model);
			normalizeUsage(output);
			if (options?.signal?.aborted) throw new Error("Request was aborted");
			void controller.requestReadback();
			stream.push({ type: "done", reason: output.stopReason as "stop" | "length" | "toolUse", message: output });
			stream.end();
		} catch (error) {
			if (controller) void controller.requestReadback();
			for (const block of output.content) delete (block as any).partialJson;
			output.stopReason = options?.signal?.aborted ? "aborted" : "error";
			output.errorMessage = error instanceof Error ? error.message : String(error);
			stream.push({ type: "error", reason: output.stopReason, error: output });
			stream.end();
		}
	})();

	return stream;
}

function promptCacheKey(sessionId?: string): string | undefined {
	if (!sessionId) return undefined;
	return sessionId.slice(0, 64);
}

const CODEX_IMAGE_MAX_BASE64_BYTES = 1.5 * 1024 * 1024;

async function sanitizeCodexBody(body: Record<string, unknown>): Promise<Record<string, unknown>> {
	const out = { ...body };
	// Pi compaction/summarization may add the standard OpenAI Responses cap, but
	// ChatGPT Codex backend rejects it with "Unsupported parameter: max_output_tokens".
	delete out.max_output_tokens;
	await shrinkCodexImages(out);
	return out;
}

async function shrinkCodexImages(node: unknown): Promise<void> {
	if (Array.isArray(node)) {
		let previousText: Record<string, unknown> | undefined;
		for (const item of node) {
			if (item && typeof item === "object") {
				const record = item as Record<string, unknown>;
				if (record.type === "input_text") previousText = record;
				if (record.type === "input_image" && typeof record.image_url === "string") {
					const resized = await shrinkDataImageUrl(record.image_url);
					record.image_url = resized.url;
					if (resized.wasShrunk && previousText && typeof previousText.text === "string") {
						previousText.text = updateImageDeliveryNote(previousText.text, resized);
					}
				}
			}
			await shrinkCodexImages(item);
		}
		return;
	}
	if (!node || typeof node !== "object") return;
	for (const value of Object.values(node as Record<string, unknown>)) await shrinkCodexImages(value);
}

interface ShrinkResult {
	url: string;
	mimeType: string;
	width: number;
	height: number;
	wasShrunk: boolean;
}

async function shrinkDataImageUrl(url: string): Promise<ShrinkResult> {
	const match = /^data:([^;,]+);base64,([\s\S]*)$/.exec(url);
	if (!match) return { url, mimeType: "image/unknown", width: 0, height: 0, wasShrunk: false };
	const mimeType = match[1];
	const data = match[2];
	if (Buffer.byteLength(data, "utf8") <= CODEX_IMAGE_MAX_BASE64_BYTES) {
		return { url, mimeType, width: 0, height: 0, wasShrunk: false };
	}
	const { resizeImage } = await import("@earendil-works/pi-coding-agent");
	const bytes = Uint8Array.from(Buffer.from(data, "base64"));
	const resized = await resizeImage(bytes, mimeType, { maxBytes: CODEX_IMAGE_MAX_BASE64_BYTES });
	if (!resized) {
		throw new Error("Image could not be resized below the Void Codex image guard; run sips -Z 1600 --setProperty format jpeg --setProperty formatOptions 70 INPUT --out OUTPUT.jpg and read OUTPUT.jpg.");
	}
	return {
		url: "data:" + resized.mimeType + ";base64," + resized.data,
		mimeType: resized.mimeType,
		width: resized.width,
		height: resized.height,
		wasShrunk: true,
	};
}

function updateImageDeliveryNote(text: string, resized: ShrinkResult): string {
	const note = /\[Image: original (\d+)x(\d+), displayed at \d+x\d+\. Multiply coordinates by [0-9.]+ to map to original image\.\]/;
	return text.replace(note, (_match, originalWidth: string, originalHeight: string) => {
		const scale = Number(originalWidth) / resized.width;
		return "[Image: original " + originalWidth + "x" + originalHeight + ", delivered " + resized.width + "x" + resized.height + " " + resized.mimeType + ". Multiply coordinates by " + scale.toFixed(2) + " to map to original image.]";
	});
}

function normalizeUsage(output: AssistantMessage) {
	const usage = output.usage;
	usage.totalTokens = usage.input + usage.output + usage.cacheRead + usage.cacheWrite;
}

async function buildCodexBody(model: Model<any>, context: Context, options?: SimpleStreamOptions): Promise<Record<string, unknown>> {
	const { convertResponsesMessages, convertResponsesTools } = await openAIResponsesShared();
	const body: Record<string, unknown> = {
		model: model.id,
		store: false,
		stream: true,
		instructions: context.systemPrompt || "You are a helpful assistant.",
		input: convertResponsesMessages(model, context, new Set(["openai", "openai-codex", "opencode"]), { includeSystemPrompt: false }),
		text: { verbosity: (options as any)?.textVerbosity || "low" },
		include: ["reasoning.encrypted_content"],
		prompt_cache_key: promptCacheKey(options?.sessionId),
		tool_choice: "auto",
		parallel_tool_calls: true,
	};
	if ((options as any)?.temperature !== undefined) body.temperature = (options as any).temperature;
	if ((options as any)?.serviceTier !== undefined) body.service_tier = (options as any).serviceTier;
	if (context.tools && context.tools.length > 0) body.tools = convertResponsesTools(context.tools, { strict: null });
	const clampedReasoning = options?.reasoning ? clampThinkingLevel(model, options.reasoning) : undefined;
	const reasoningEffort = clampedReasoning === "off" ? undefined : clampedReasoning;
	if (reasoningEffort !== undefined) {
		const effort = reasoningEffort === "none"
			? (model.thinkingLevelMap?.off ?? "none")
			: (model.thinkingLevelMap?.[reasoningEffort] ?? reasoningEffort);
		if (effort !== null) body.reasoning = { effort, summary: (options as any)?.reasoningSummary ?? "auto" };
	}
	return body;
}

// Use Pi's own request/stream helpers. Native child loaders can expose virtual modules even
// when PI_PACKAGE_DIR is unset, so failure of import.meta.resolve does not identify a desktop
// bundle. Prefer loader aliases, retain the desktop's explicit vendor contract, and otherwise
// resolve the dependency from the running Pi package (not the extension directory or PATH).
async function openAIResponsesShared(): Promise<any> {
	let resolveFailure: unknown;
	try {
		const compatUrl = await import.meta.resolve("@earendil-works/pi-ai/compat");
		return await import(new URL("./api/openai-responses-shared.js", compatUrl).href);
	} catch (error) {
		resolveFailure = error;
	}
	const packageDir = process.env.PI_PACKAGE_DIR;
	const because = resolveFailure instanceof Error ? resolveFailure.message : String(resolveFailure);
	if (!packageDir) {
		try {
			// pi-ai has import-only exports. Locate its package along the runtime's ancestor
			// dependency directories, including hoisting, but exclude NODE_PATH/global modules.
			let directory = getPackageDir();
			for (;;) {
				const dependency = path.join(directory, "node_modules/@earendil-works/pi-ai");
				if (existsSync(path.join(dependency, "package.json"))) {
					return await import(pathToFileURL(path.join(dependency, "dist/api/openai-responses-shared.js")).href);
				}
				const parent = path.dirname(directory);
				if (parent === directory) throw new Error("running Pi's pi-ai dependency is missing");
				directory = parent;
			}
		} catch (error) {
			const runtimeFailure = error instanceof Error ? error.message : String(error);
			throw new Error("cannot assemble the model's answer: Pi's Responses helpers are unavailable in the running runtime. Runtime: " + runtimeFailure + ". Resolver: " + because);
		}
	}
	try {
		return await import(new URL("./vendor/pi-ai/api/openai-responses-shared.js", pathToFileURL(packageDir + path.sep)).href);
	} catch (error) {
		const alsoBecause = error instanceof Error ? error.message : String(error);
		throw new Error("cannot assemble the model's answer: Pi's Responses helpers are missing from the bundled runtime at " + packageDir + " and could not be resolved either. Chat will not work until the application is reinstalled. Bundled copy: " + alsoBecause + ". Resolver: " + because);
	}
}

async function* parseSSE(response: Response, signal?: AbortSignal): AsyncGenerator<any> {
	const reader = response.body!.getReader();
	const decoder = new TextDecoder();
	let buffer = "";
	try {
		while (true) {
			if (signal?.aborted) throw new Error("Request was aborted");
			const { done, value } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });
			let idx = buffer.indexOf("\n\n");
			while (idx >= 0) {
				const chunk = buffer.slice(0, idx);
				buffer = buffer.slice(idx + 2);
				const data = chunk.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trim()).join("\n").trim();
				if (data && data !== "[DONE]") yield normalizeSSEError(JSON.parse(data));
				idx = buffer.indexOf("\n\n");
			}
		}
	} finally {
		try { reader.releaseLock(); } catch {}
	}
}

function normalizeSSEError(event: any): any {
	if (event?.type !== "error" || !event.error || typeof event.error !== "object") return event;
	return {
		...event,
		code: event.code ?? event.error.code,
		message: event.message ?? event.error.message,
	};
}

function headersToRecord(headers: Headers): Record<string, string> {
	const out: Record<string, string> = {};
	headers.forEach((value, key) => { out[key] = value; });
	return out;
}

// ---- Pure model-decision authority -------------------------------------------------
// This section has no clock reads, I/O, Pi calls or controller-owned mutable state.
// It is exported through pi_model_decision.ts, but ships in this standalone file.
const NS_PER_SECOND = 1_000_000_000n;
const MAX_I64 = 9223372036854775807n;
const STATE_ENTRY = "void-client-model-state-v1";
export interface ModelDecisionConfig {
	schemaVersion: 1; readbackUrl: string; pollIntervalSeconds: string;
	catalogDecisionTtlSeconds: string; catalogExpirySkewSeconds: string;
}
interface ModelAuthority {
	effectiveAssignmentRevision: string; assignmentHeadRevision: string;
	scheduledSuccessor: null | { assignmentRevision: string; effectiveAt: string; tierModelSetDigest: string };
	policyRevision: string; tierId: string; tierModelSetDigest: string; calibrationRevision: string;
	providerGrantSetRevision: string; poolRevision: string; poolCollectionRevision: string;
	controlRevision: string; controlEpoch: string; quotaLatchRevision: string; quotaEpisode: string | null;
	inputFingerprint: string; controlMode: "shadow" | "warn" | "active";
	quotaState: "normal" | "warning" | "fallback"; restrictionActive: boolean;
	allowedCodexModelIds: string[]; defaultCodexModelId: string | null;
	fallbackCodexModelId: string; effectiveCodexModelId: string | null;
}
export interface ClientModelDecision {
	schemaVersion: 1; generation: string; outcome: "catalog" | "empty_compatible_catalog";
	evaluatedAt: string; validUntil: string; authority: ModelAuthority; display?: unknown;
}
interface LegacyRestriction {
	generation: string; quotaState: "fallback"; policyRevision: string; quotaEpisode: string;
	restricted: true; effectiveModel: string; allowedCodexModelIds: string[];
}
type AuthorityStatus = "initial_closed" | "active" | "authoritative_empty" | "legacy_restrictive_pending" | "quarantined" | "expired";
interface Safety {
	highestSeenGeneration: string | null; authorityStatus: AuthorityStatus;
	acceptedAuthority: ModelAuthority | null;
	lastLeaseEvidence: { evaluatedAt: string; validUntil: string } | null;
	legacyRestriction: LegacyRestriction | null;
}
export interface EffectTarget {
	protocolVersion: 1; branchContextEpoch: bigint;
	targetKind: "active_catalog" | "legacy_restrictive_catalog" | "closed_catalog";
	safetyGeneration: string | null; inputFingerprint: string | null; authorityDigest: string | null;
	legacyProjectionDigest: string | null; catalogDigest: string; selectionModelId: string | null;
}
export interface EffectToken {
	target: EffectTarget; controllerInstanceId: string; planOrdinal: bigint; attemptOrdinal: bigint;
}
export interface ClientModelState extends Safety {
	controllerInstanceId: string; stateRevision: bigint; branchContextEpoch: bigint; lastAllocatedOrdinal: bigint;
	compatibilityModels: Model<any>[]; config: ModelDecisionConfig | null;
	leaseDeadline: bigint | null; authoritativeRefreshRequired: boolean; preRestrictionModelId: string | null;
	pendingEffectPlan: EffectToken | null; desiredTarget: EffectTarget; orderedModels: Model<any>[];
	selectableModelIds: string[]; appliedEffects: { status: "closed" | "applying" | "applied" | "effect_failed_closed"; target?: EffectTarget };
	currentModel: { provider: string; id: string } | null; restoreMemoryOnSuccess: boolean;
	restored: boolean; freshReadback: boolean; retired: boolean;
}
// Events arrive from an untrusted in-process boundary as well as from wire codecs.
// A closed validator below admits each event before its fields can affect authority.
export interface ClientEvent { type: string; [key: string]: any }
export interface ControllerCommit {
	protocolVersion: 1; controllerInstanceId: string; baseStateRevision: bigint; branchContextEpoch: bigint;
	commitMonoNs: bigint; event: ClientEvent;
	allocation: { kind: "none" } | { kind: "apply_pi_catalog"; token: EffectToken };
}
interface PiIntent { target: EffectTarget; orderedModels: Model<any>[]; selectionModelId: string | null }
type DecisionEffect = { type: "applyPiCatalog"; token: EffectToken; orderedModels: Model<any>[]; selectionModelId: string | null }
	| { type: "requestReadback" }
	| { type: "checkpoint"; writeReason: "safety_commit" | "preference_commit" | "branch_rebase" | "shutdown" }
	| { type: "diagnostic"; stage: string; boundedErrorClass: string };
interface Transition { state: ClientModelState; piIntent: PiIntent | null; effects: DecisionEffect[] }
const revisionKeys = ["effectiveAssignmentRevision", "assignmentHeadRevision", "policyRevision", "calibrationRevision", "providerGrantSetRevision", "poolRevision", "poolCollectionRevision", "controlRevision", "controlEpoch", "quotaLatchRevision"] as const;
const authorityKeys = [...revisionKeys, "scheduledSuccessor", "tierId", "tierModelSetDigest", "quotaEpisode", "inputFingerprint", "controlMode", "quotaState", "restrictionActive", "allowedCodexModelIds", "defaultCodexModelId", "fallbackCodexModelId", "effectiveCodexModelId"];
const headerNames = ["x-void-model-decision-version", "x-void-model-decision", "x-void-quota-state", "x-void-effective-model", "x-void-model-decision-generation", "x-void-quota-policy-revision", "x-void-quota-episode", "x-void-model-selection-restricted", "x-void-allowed-codex-models"] as const;
type HeaderSlot = { kind: "absent" | "over_limit" | "unreadable" } | { kind: "value"; combined: string };
type HeaderSnapshot = Record<string, HeaderSlot>;
function object(value: unknown): value is Record<string, any> { return value !== null && typeof value === "object" && !Array.isArray(value); }
function closed(value: unknown, keys: readonly string[], optional: readonly string[] = []): value is Record<string, any> {
	return object(value) && keys.every(k => Object.hasOwn(value, k)) && Object.keys(value).every(k => keys.includes(k) || optional.includes(k));
}
function boundedString(value: unknown, max = 256): value is string {
	return typeof value === "string" && value.length > 0 && Buffer.byteLength(value, "utf8") <= max;
}
function generation(value: unknown): value is string {
	return typeof value === "string" && /^[1-9][0-9]{0,18}$/.test(value) && (value.length < 19 || value <= "9223372036854775807");
}
function compareGeneration(a: string, b: string): number { return a.length === b.length ? (a === b ? 0 : a < b ? -1 : 1) : a.length < b.length ? -1 : 1; }
function instant(value: unknown): value is bigint { return typeof value === "bigint" && value >= 0n && value <= MAX_I64; }
function freeze<T>(value: T): T {
	if (value && typeof value === "object" && !Object.isFrozen(value)) {
		for (const child of Object.values(value)) freeze(child);
		Object.freeze(value);
	}
	return value;
}
export function canonicalSerialize(value: unknown): string {
	if (value === null || typeof value === "string" || typeof value === "boolean") return JSON.stringify(value);
	if (typeof value === "number" && Number.isFinite(value)) return JSON.stringify(value);
	if (Array.isArray(value)) return "[" + value.map(canonicalSerialize).join(",") + "]";
	if (object(value)) return "{" + Object.keys(value).sort().map(key => JSON.stringify(key) + ":" + canonicalSerialize(value[key])).join(",") + "}";
	throw new Error("Noncanonical wire value");
}
function digest(domain: "authority" | "catalog" | "legacy", value: unknown): string {
	return "v1:" + createHash("sha256").update(canonicalSerialize({ domain, value, version: 1 }), "utf8").digest("hex");
}
function equal(a: unknown, b: unknown): boolean { return isDeepStrictEqual(a, b); }
function timestamp(value: unknown): bigint | null {
	if (typeof value !== "string") return null;
	const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})\.(\d{9})Z$/.exec(value);
	if (!match) return null;
	const date = new Date(match[1] + ".000Z");
	const ms = date.getTime();
	if (!Number.isFinite(ms) || date.toISOString() !== match[1] + ".000Z") return null;
	const ns = BigInt(ms) * 1_000_000n + BigInt(match[2]);
	return ns >= -MAX_I64 - 1n && ns <= MAX_I64 ? ns : null;
}
function duration(value: unknown, max: bigint, min = 0n): bigint | null {
	if (typeof value !== "string" || !/^(0|[1-9][0-9]{0,9})$/.test(value)) return null;
	const n = BigInt(value);
	return n >= min && n <= max ? n : null;
}
function parseConfig(value: unknown): ModelDecisionConfig | null {
	if (!closed(value, ["schemaVersion", "readbackUrl", "pollIntervalSeconds", "catalogDecisionTtlSeconds", "catalogExpirySkewSeconds"]) || value.schemaVersion !== 1 || typeof value.readbackUrl !== "string") return null;
	try { const url = new URL(value.readbackUrl); if (!["https:", "http:"].includes(url.protocol) || !url.hostname || url.username || url.password) return null; } catch { return null; }
	const poll = duration(value.pollIntervalSeconds, 300n, 1n);
	const ttl = duration(value.catalogDecisionTtlSeconds, 2147483647n, 1n);
	const skew = duration(value.catalogExpirySkewSeconds, 2147483647n);
	if (poll === null || ttl === null || skew === null || skew >= ttl) return null;
	return structuredClone(value) as ModelDecisionConfig;
}
export function parseBootstrap(input: unknown): { ok: false } | { ok: true; bootstrap: Bootstrap & { modelDecision: ModelDecisionConfig } } {
	if (!closed(input, ["version", "relayUrl", "authToken", "providers", "modelDecision"]) || input.version !== 2 || !boundedString(input.relayUrl, 8192) || !boundedString(input.authToken, 16384) || !Array.isArray(input.providers)) return { ok: false };
	if (!input.providers.every(p => closed(p, ["kind", "relayProviderId", "models"]) && p.kind === "codex" && boundedString(p.relayProviderId) && Array.isArray(p.models) && p.models.length <= 64 && p.models.every((id: unknown) => boundedString(id, 128)))) return { ok: false };
	const config = parseConfig(input.modelDecision);
	if (!config) return { ok: false };
	return { ok: true, bootstrap: structuredClone(input) as Bootstrap & { modelDecision: ModelDecisionConfig } };
}
function validAuthority(value: unknown, outcome: unknown): value is ModelAuthority {
	if (!closed(value, authorityKeys) || !revisionKeys.every(k => generation(value[k]))) return false;
	const a = value;
	if (a.scheduledSuccessor !== null && (!closed(a.scheduledSuccessor, ["assignmentRevision", "effectiveAt", "tierModelSetDigest"]) || !generation(a.scheduledSuccessor.assignmentRevision) || timestamp(a.scheduledSuccessor.effectiveAt) === null || !boundedString(a.scheduledSuccessor.tierModelSetDigest))) return false;
	if (!["tierId", "tierModelSetDigest", "inputFingerprint"].every(k => boundedString(a[k])) || !(a.quotaEpisode === null || generation(a.quotaEpisode))) return false;
	if (!["shadow", "warn", "active"].includes(a.controlMode) || !["normal", "warning", "fallback"].includes(a.quotaState) || a.restrictionActive !== (a.controlMode === "active" && a.quotaState === "fallback")) return false;
	if (!boundedString(a.fallbackCodexModelId, 128) || !Array.isArray(a.allowedCodexModelIds) || a.allowedCodexModelIds.length > 64 || !a.allowedCodexModelIds.every((id: unknown) => boundedString(id, 128)) || new Set(a.allowedCodexModelIds).size !== a.allowedCodexModelIds.length) return false;
	if (outcome === "empty_compatible_catalog") return a.allowedCodexModelIds.length === 0 && a.defaultCodexModelId === null && a.effectiveCodexModelId === null;
	if (outcome !== "catalog" || a.allowedCodexModelIds.length === 0 || !a.allowedCodexModelIds.includes(a.defaultCodexModelId) || a.effectiveCodexModelId !== a.defaultCodexModelId) return false;
	return !a.restrictionActive || (a.allowedCodexModelIds.length === 1 && a.allowedCodexModelIds[0] === a.fallbackCodexModelId && a.defaultCodexModelId === a.fallbackCodexModelId);
}
function outcomeOf(a: ModelAuthority): ClientModelDecision["outcome"] { return a.allowedCodexModelIds.length ? "catalog" : "empty_compatible_catalog"; }
function compatibleModel(models: Model<any>[], id: string): Model<any> | null {
	const matches = models.filter(m => m.id === id);
	if (matches.length !== 1) return null;
	const m = matches[0];
	if (!boundedString(m.name) || typeof m.reasoning !== "boolean" || !Array.isArray(m.input) || !m.input.length || !m.input.every(v => v === "text" || v === "image") || !object(m.cost) || ![m.cost.input, m.cost.output, m.cost.cacheRead, m.cost.cacheWrite, m.contextWindow, m.maxTokens].every(v => typeof v === "number" && Number.isFinite(v) && v >= 0)) return null;
	try { digest("catalog", [m]); return m; } catch { return null; }
}
function historicalTtl(evidence: { evaluatedAt: string; validUntil: string }, config: ModelDecisionConfig | null): bigint | null {
	const start = timestamp(evidence.evaluatedAt), end = timestamp(evidence.validUntil);
	if (start === null || end === null || !config) return null;
	const ttl = end - start;
	return ttl > 0n && ttl <= MAX_I64 && ttl <= BigInt(config.catalogDecisionTtlSeconds) * NS_PER_SECOND ? ttl : null;
}
export function parseDecision(input: unknown, models: Model<any>[], configuration: unknown): { ok: false } | { ok: true; decision: ClientModelDecision } {
	const config = parseConfig(configuration);
	if (!closed(input, ["schemaVersion", "generation", "outcome", "evaluatedAt", "validUntil", "authority"], ["display"]) || input.schemaVersion !== 1 || !generation(input.generation) || !validAuthority(input.authority, input.outcome) || historicalTtl(input as ClientModelDecision, config) === null) return { ok: false };
	try { if (Buffer.byteLength(canonicalSerialize(input), "utf8") > 16384) return { ok: false }; } catch { return { ok: false }; }
	if (!input.authority.allowedCodexModelIds.every(id => compatibleModel(models, id))) return { ok: false };
	return { ok: true, decision: structuredClone(input) as ClientModelDecision };
}
function lease(d: ClientModelDecision, config: ModelDecisionConfig | null, start: unknown, receipt: unknown): bigint | null {
	if (!config || !instant(start) || !instant(receipt) || receipt < start) return null;
	const ttl = historicalTtl(d, config);
	if (ttl === null) return null;
	const remaining = ttl - (receipt - start) - BigInt(config.catalogExpirySkewSeconds) * NS_PER_SECOND;
	if (remaining <= 0n || remaining > MAX_I64 || receipt > MAX_I64 - remaining) return null;
	return receipt + remaining;
}
export function snapshotDecisionHeaders(headers: Pick<Headers, "has" | "get">): HeaderSnapshot {
	const snapshot: HeaderSnapshot = {};
	for (const name of headerNames) {
		let has: unknown, value: unknown, failed = false;
		try { has = headers.has(name); } catch { failed = true; }
		try { value = headers.get(name); } catch { failed = true; }
		snapshot[name] = failed ? { kind: "unreadable" }
			: has === false && value === null ? { kind: "absent" }
			: has === true && typeof value === "string" ? (Buffer.byteLength(value, "utf8") > 21848 ? { kind: "over_limit" } : { kind: "value", combined: value })
			: { kind: "unreadable" };
	}
	return freeze(snapshot);
}
function headerValue(h: HeaderSnapshot, i: number): string | undefined { const slot = h[headerNames[i]]; return slot?.kind === "value" ? slot.combined : undefined; }
function headerModel(value: unknown): value is string { return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/.test(value); }
function headerList(value: string | undefined): string[] | null {
	if (value === undefined || value.length > 8255) return null;
	const ids = value.split(","); // A complete diagnostic list, never split-and-pick a scalar.
	return ids.length <= 64 && ids.every(headerModel) && new Set(ids).size === ids.length ? ids : null;
}
function legacyValid(value: unknown): value is LegacyRestriction {
	return closed(value, ["generation", "quotaState", "policyRevision", "quotaEpisode", "restricted", "effectiveModel", "allowedCodexModelIds"]) && generation(value.generation) && value.quotaState === "fallback" && generation(value.policyRevision) && generation(value.quotaEpisode) && value.restricted === true && headerModel(value.effectiveModel) && equal(value.allowedCodexModelIds, [value.effectiveModel]);
}
function legacyAgrees(l: LegacyRestriction, d: { generation: string; authority: ModelAuthority }): boolean {
	const a = d.authority;
	return l.generation === d.generation && l.quotaState === a.quotaState && l.policyRevision === a.policyRevision && l.quotaEpisode === a.quotaEpisode && l.restricted === a.restrictionActive && l.effectiveModel === a.effectiveCodexModelId && equal(l.allowedCodexModelIds, a.allowedCodexModelIds);
}
interface HeaderInput { decision?: unknown; legacy?: LegacyRestriction; floor: string | null; close: boolean; refresh: boolean }
function parseHeaderInput(input: unknown, models: Model<any>[], config: ModelDecisionConfig | null): HeaderInput {
	const invalid: HeaderInput = { floor: null, close: true, refresh: true };
	if (!closed(input, headerNames)) return invalid;
	for (const slot of Object.values(input)) {
		if (!object(slot) || (slot.kind === "value" ? !closed(slot, ["kind", "combined"]) || typeof slot.combined !== "string" || Buffer.byteLength(slot.combined, "utf8") > 21848 : !closed(slot, ["kind"]) || !["absent", "over_limit", "unreadable"].includes(slot.kind))) return invalid;
	}
	const h = input as HeaderSnapshot;
	const values = headerNames.map((_, i) => headerValue(h, i));
	const [version, payload, quota, model, g, policy, episode, restricted, allowed] = values;
	let decoded: unknown;
	if (payload && /^[A-Za-z0-9_-]+$/.test(payload)) {
		try {
			const bytes = Buffer.from(payload, "base64url");
			if (bytes.length <= 16384 && bytes.toString("base64url") === payload) {
				const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
				decoded = JSON.parse(text);
				if (canonicalSerialize(decoded) !== text) decoded = undefined;
			}
		} catch { decoded = undefined; }
	}
	const encodedFloor = object(decoded) && generation(decoded.generation) ? decoded.generation : null;
	const legacyFloor = generation(g) ? g : null;
	const parsed = version === "1" ? parseDecision(decoded, models, config) : { ok: false as const };
	if (parsed.ok) {
		const d = parsed.decision, a = d.authority;
		const expected: unknown[] = [a.quotaState, a.effectiveCodexModelId, d.generation, a.policyRevision, a.quotaEpisode, String(a.restrictionActive), a.allowedCodexModelIds];
		let agree = true;
		for (let i = 2; i < 9; i++) {
			if (h[headerNames[i]].kind === "absent") continue;
			const v = values[i];
			const valid = i === 2 ? ["normal", "warning", "fallback"].includes(v ?? "") : i === 3 ? headerModel(v) : i >= 4 && i <= 6 ? generation(v) : i === 7 ? v === "true" || v === "false" : headerList(v) !== null;
			if (!valid || !equal(i === 8 ? headerList(v) : v, expected[i - 2])) agree = false;
		}
		return agree ? { decision: d, floor: d.generation, close: false, refresh: false } : { floor: d.generation, close: true, refresh: true };
	}
	const projection = { generation: g, quotaState: quota, policyRevision: policy, quotaEpisode: episode, restricted: restricted === "true" ? true : false, effectiveModel: model, allowedCodexModelIds: allowed === model ? [model] : [] };
	const complete = legacyValid(projection) && values.slice(2).every(v => typeof v === "string") && Buffer.byteLength(values.slice(2).join(""), "utf8") <= 1024;
	if (encodedFloor && legacyFloor && encodedFloor !== legacyFloor) return { floor: compareGeneration(encodedFloor, legacyFloor) > 0 ? encodedFloor : legacyFloor, close: true, refresh: true };
	if (complete) {
		// Even an invalid encoded object can prove a conflict, never permission.
		if (object(decoded) && object(decoded.authority)) {
			const a = decoded.authority;
			const overlaps = { quotaState: quota, policyRevision: policy, quotaEpisode: episode, restrictionActive: true, effectiveCodexModelId: model, allowedCodexModelIds: [model] };
			if (Object.entries(overlaps).some(([k, v]) => Object.hasOwn(a, k) && !equal(a[k], v))) return { floor: encodedFloor ?? legacyFloor, close: true, refresh: true };
		}
		return { legacy: projection as LegacyRestriction, floor: legacyFloor, close: false, refresh: true };
	}
	const encodedPresent = h[headerNames[0]].kind !== "absent" || h[headerNames[1]].kind !== "absent";
	const anyLegacy = headerNames.slice(2).some(name => h[name].kind !== "absent");
	const nonFallback = (quota === "normal" || quota === "warning") && restricted === "false";
	return { floor: encodedFloor ?? legacyFloor, close: encodedPresent || (anyLegacy && !nonFallback), refresh: true };
}
function emptySafety(): Safety { return { highestSeenGeneration: null, authorityStatus: "initial_closed", acceptedAuthority: null, lastLeaseEvidence: null, legacyRestriction: null }; }
function quarantined(g: string): Safety { return { highestSeenGeneration: g, authorityStatus: "quarantined", acceptedAuthority: null, lastLeaseEvidence: null, legacyRestriction: null }; }
function safetyOf(s: Safety): Safety {
	if (s.authorityStatus === "quarantined" && s.highestSeenGeneration) return quarantined(s.highestSeenGeneration);
	if (s.authorityStatus === "legacy_restrictive_pending") return { highestSeenGeneration: s.highestSeenGeneration, authorityStatus: s.authorityStatus, acceptedAuthority: null, lastLeaseEvidence: null, legacyRestriction: s.legacyRestriction };
	return { highestSeenGeneration: s.highestSeenGeneration, authorityStatus: s.authorityStatus, acceptedAuthority: s.acceptedAuthority, lastLeaseEvidence: s.lastLeaseEvidence, legacyRestriction: null };
}
function joinSafety(left: Safety, right: Safety, ordered = true): Safety {
	if (right.highestSeenGeneration === null) return left;
	if (left.highestSeenGeneration === null) return right;
	const order = compareGeneration(right.highestSeenGeneration, left.highestSeenGeneration);
	if (order !== 0) return order > 0 ? right : left;
	const g = left.highestSeenGeneration;
	if (left.authorityStatus === "quarantined" || right.authorityStatus === "quarantined") return quarantined(g);
	if (left.legacyRestriction || right.legacyRestriction) {
		if (left.legacyRestriction && right.legacyRestriction) return equal(left.legacyRestriction, right.legacyRestriction) ? right : quarantined(g);
		const pending = left.legacyRestriction ? left : right, full = left.legacyRestriction ? right : left;
		if (!full.acceptedAuthority || !legacyAgrees(pending.legacyRestriction!, { generation: g, authority: full.acceptedAuthority })) return quarantined(g);
		return ordered ? right : pending;
	}
	if (!equal(left.acceptedAuthority, right.acceptedAuthority)) return quarantined(g);
	const a = left.lastLeaseEvidence, b = right.lastLeaseEvidence;
	if (!a || !b) return quarantined(g);
	if (a.evaluatedAt === b.evaluatedAt && a.validUntil !== b.validUntil) return quarantined(g);
	const chosen = b.evaluatedAt > a.evaluatedAt ? right : left;
	return left.authorityStatus === "expired" || right.authorityStatus === "expired" ? { ...chosen, authorityStatus: "expired" } : chosen;
}
function checkpointProjection(data: unknown, entry: Record<string, any>, sessionId: string, config: ModelDecisionConfig | null): { safety: Safety; preference: string | null; full: boolean } | null {
	if (!closed(data, ["schemaVersion", "branchProof", "safety", "preference"]) || data.schemaVersion !== 1) return null;
	try { if (Buffer.byteLength(canonicalSerialize(data), "utf8") > 32768) return null; } catch { return null; }
	const s = data.safety;
	if (!closed(s, ["highestSeenGeneration", "authorityStatus", "acceptedAuthority", "lastLeaseEvidence", "legacyRestriction"])) return null;
	const g = s.highestSeenGeneration;
	if (g !== null && !generation(g)) return null;
	let durationProved = true;
	switch (s.authorityStatus) {
		case "initial_closed": if (g !== null || s.acceptedAuthority !== null || s.lastLeaseEvidence !== null || s.legacyRestriction !== null) return null; break;
		case "quarantined": if (g === null || s.acceptedAuthority !== null || s.lastLeaseEvidence !== null || s.legacyRestriction !== null) return null; break;
		case "legacy_restrictive_pending": if (g === null || s.acceptedAuthority !== null || s.lastLeaseEvidence !== null || !legacyValid(s.legacyRestriction) || s.legacyRestriction.generation !== g) return null; break;
		case "active": case "authoritative_empty": case "expired": {
			if (g === null || !object(s.acceptedAuthority) || s.legacyRestriction !== null || !closed(s.lastLeaseEvidence, ["evaluatedAt", "validUntil"])) return null;
			const outcome = s.authorityStatus === "authoritative_empty" ? "empty_compatible_catalog" : s.authorityStatus === "active" ? "catalog" : s.acceptedAuthority.allowedCodexModelIds?.length ? "catalog" : "empty_compatible_catalog";
			if (!validAuthority(s.acceptedAuthority, outcome) || timestamp(s.lastLeaseEvidence.evaluatedAt) === null || timestamp(s.lastLeaseEvidence.validUntil) === null) return null;
			durationProved = historicalTtl(s.lastLeaseEvidence, config) !== null;
			break;
		}
		default: return null;
	}
	const proof = data.branchProof;
	const bound = closed(proof, ["sessionId", "parentEntryId", "writeReason"]) && boundedString(proof.sessionId) && (proof.parentEntryId === null || boundedString(proof.parentEntryId)) && ["safety_commit", "preference_commit", "branch_rebase", "shutdown"].includes(proof.writeReason) && proof.sessionId === sessionId && proof.parentEntryId === entry.parentId;
	const preference = data.preference;
	const validPreference = closed(preference, ["preRestrictionModelId"]) && (preference.preRestrictionModelId === null || boundedString(preference.preRestrictionModelId, 128));
	return { safety: !bound || !durationProved ? (g === null ? emptySafety() : quarantined(g)) : structuredClone(s) as Safety, preference: bound && durationProved && validPreference ? preference.preRestrictionModelId : null, full: !!(bound && durationProved && validPreference) };
}
function restoreBranch(snapshot: unknown, config: ModelDecisionConfig | null): { safety: Safety; preference: string | null } {
	let safety = emptySafety(), preference: string | null = null;
	if (!closed(snapshot, ["sessionId", "leafId", "entries"]) || !boundedString(snapshot.sessionId) || !Array.isArray(snapshot.entries)) return { safety, preference };
	const seen = new Set<string>(); let parent: string | null = null, validPath = true;
	for (const entry of snapshot.entries) {
		if (!object(entry) || !boundedString(entry.id) || seen.has(entry.id) || entry.parentId !== parent) { validPath = false; break; }
		seen.add(entry.id); parent = entry.id;
		if (entry.type !== "custom" || entry.customType !== STATE_ENTRY) continue;
		const candidate = checkpointProjection(entry.data, entry, snapshot.sessionId, config);
		// Invalid candidate is no contribution, never a reset of previously proved safety.
		if (candidate) safety = joinSafety(safety, candidate.safety);
		preference = candidate?.full ? candidate.preference : null;
	}
	if (parent !== snapshot.leafId || !validPath) preference = null;
	return { safety, preference };
}
function targetFor(s: ClientModelState): PiIntent {
	let kind: EffectTarget["targetKind"] = "closed_catalog", models: Model<any>[] = [], selection: string | null = null;
	const a = s.acceptedAuthority;
	if (s.authorityStatus === "active" && a && s.leaseDeadline !== null && !s.authoritativeRefreshRequired && s.freshReadback) {
		const compatible = a.allowedCodexModelIds.map(id => compatibleModel(s.compatibilityModels, id));
		if (compatible.every(m => m !== null)) { kind = "active_catalog"; models = compatible as Model<any>[]; selection = s.desiredTarget.selectionModelId; }
	} else if (s.authorityStatus === "legacy_restrictive_pending" && s.legacyRestriction) {
		const model = compatibleModel(s.compatibilityModels, s.legacyRestriction.effectiveModel);
		if (model) { kind = "legacy_restrictive_catalog"; models = [model]; selection = model.id; }
	}
	if (kind === "active_catalog" && !models.some(m => m.id === selection)) selection = a!.defaultCodexModelId;
	const complete = a && (s.authorityStatus === "active" || s.authorityStatus === "authoritative_empty" || s.authorityStatus === "expired");
	const target: EffectTarget = { protocolVersion: 1, branchContextEpoch: s.branchContextEpoch, targetKind: kind, safetyGeneration: s.highestSeenGeneration,
		inputFingerprint: complete ? a.inputFingerprint : null, authorityDigest: complete ? digest("authority", { outcome: outcomeOf(a), authority: a }) : null,
		legacyProjectionDigest: kind === "legacy_restrictive_catalog" ? digest("legacy", s.legacyRestriction) : null,
		catalogDigest: digest("catalog", models), selectionModelId: selection };
	return { target, orderedModels: models, selectionModelId: selection };
}
export function newClientModelState(options: { controllerInstanceId: string; compatibilityModels: Model<any>[]; config: unknown; currentModel?: { provider: string; id: string } }): ClientModelState {
	const target: EffectTarget = { protocolVersion: 1, branchContextEpoch: 0n, targetKind: "closed_catalog", safetyGeneration: null, inputFingerprint: null, authorityDigest: null, legacyProjectionDigest: null, catalogDigest: digest("catalog", []), selectionModelId: null };
	return freeze({ ...emptySafety(), controllerInstanceId: options.controllerInstanceId, stateRevision: 0n, branchContextEpoch: 0n, lastAllocatedOrdinal: 0n,
		compatibilityModels: structuredClone(options.compatibilityModels), config: parseConfig(options.config), currentModel: options.currentModel ? { provider: options.currentModel.provider, id: options.currentModel.id } : null,
		leaseDeadline: null, authoritativeRefreshRequired: false, preRestrictionModelId: null, pendingEffectPlan: null,
		desiredTarget: target, orderedModels: [], selectableModelIds: [], appliedEffects: { status: "closed" }, restoreMemoryOnSuccess: false, restored: false, freshReadback: false, retired: false });
}
function closeApplication(s: ClientModelState, failed = false): void { s.selectableModelIds = []; s.pendingEffectPlan = null; s.appliedEffects = { status: failed ? "effect_failed_closed" : "closed" }; }
function quarantineState(s: ClientModelState, g: string): void { Object.assign(s, quarantined(g)); closeApplication(s); }
function captureRestriction(s: ClientModelState, previous: ClientModelState): void {
	if (previous.acceptedAuthority?.restrictionActive === false && permitted(previous, CODEX_PROVIDER_ID, previous.currentModel?.id ?? "", null)) s.preRestrictionModelId = previous.currentModel!.id;
}
function acceptFull(s: ClientModelState, before: ClientModelState, input: unknown, event: ClientEvent, now: bigint): void {
	if (!object(input) || !generation(input.generation)) return;
	const g = input.generation, order = s.highestSeenGeneration === null ? 1 : compareGeneration(g, s.highestSeenGeneration);
	if (order < 0 || (order === 0 && s.authorityStatus === "quarantined")) return;
	const parsed = parseDecision(input, s.compatibilityModels, event.config);
	const deadline = parsed.ok ? lease(parsed.decision, parseConfig(event.config), event.requestStartedMonoNs, event.responseReceivedMonoNs) : null;
	if (!parsed.ok || deadline === null || !instant(now)) { quarantineState(s, g); return; }
	const d = parsed.decision, a = d.authority;
	const pendingCompletion = order === 0 && s.authorityStatus === "legacy_restrictive_pending";
	if (pendingCompletion && (!s.legacyRestriction || !legacyAgrees(s.legacyRestriction, d))) { quarantineState(s, g); return; }
	if (order === 0 && !pendingCompletion && s.acceptedAuthority) {
		if (!equal(s.acceptedAuthority, a) || outcomeOf(s.acceptedAuthority) !== d.outcome) { quarantineState(s, g); return; }
		const evidence = s.lastLeaseEvidence!;
		if (d.evaluatedAt < evidence.evaluatedAt) return;
		if (d.evaluatedAt === evidence.evaluatedAt) {
			if (d.validUntil !== evidence.validUntil) { quarantineState(s, g); return; }
			// Exact evidence cannot acquire a new deadline, even after restart.
			if (s.leaseDeadline !== null && s.authorityStatus === "active" && s.authoritativeRefreshRequired) { s.authoritativeRefreshRequired = false; closeApplication(s); }
			return;
		}
	}
	if (event.source === "admission_header" && !s.freshReadback) return;
	const previousRestricted = before.authorityStatus === "legacy_restrictive_pending" || before.acceptedAuthority?.restrictionActive === true;
	if (a.restrictionActive && !previousRestricted) captureRestriction(s, before);
	const restoring = !a.restrictionActive && (previousRestricted || s.preRestrictionModelId !== null);
	const selection = a.restrictionActive ? a.fallbackCodexModelId
		: restoring && s.preRestrictionModelId && a.allowedCodexModelIds.includes(s.preRestrictionModelId) ? s.preRestrictionModelId
		: s.highestSeenGeneration !== null && order > 0 ? a.defaultCodexModelId : !restoring && s.currentModel?.provider === CODEX_PROVIDER_ID && a.allowedCodexModelIds.includes(s.currentModel.id) ? s.currentModel.id : a.defaultCodexModelId;
	s.highestSeenGeneration = g; s.authorityStatus = d.outcome === "catalog" ? "active" : "authoritative_empty";
	s.acceptedAuthority = a; s.lastLeaseEvidence = { evaluatedAt: d.evaluatedAt, validUntil: d.validUntil };
	s.legacyRestriction = null; s.authoritativeRefreshRequired = false; s.leaseDeadline = deadline;
	if (event.source === "readback") s.freshReadback = true;
	s.desiredTarget = { ...s.desiredTarget, selectionModelId: selection };
	s.restoreMemoryOnSuccess = restoring;
}
function acceptLegacy(s: ClientModelState, before: ClientModelState, l: LegacyRestriction): void {
	const order = s.highestSeenGeneration === null ? 1 : compareGeneration(l.generation, s.highestSeenGeneration);
	if (order < 0) return;
	if (order === 0) {
		if (["quarantined", "authoritative_empty", "expired"].includes(s.authorityStatus)) return;
		if ((s.legacyRestriction && !equal(s.legacyRestriction, l)) || (!s.legacyRestriction && s.acceptedAuthority && !legacyAgrees(l, { generation: l.generation, authority: s.acceptedAuthority }))) { quarantineState(s, l.generation); s.authoritativeRefreshRequired = true; return; }
	}
	if (s.authorityStatus !== "legacy_restrictive_pending" && !s.acceptedAuthority?.restrictionActive) captureRestriction(s, before);
	s.highestSeenGeneration = l.generation; s.authorityStatus = "legacy_restrictive_pending";
	s.legacyRestriction = l; s.authoritativeRefreshRequired = true;
	// Lower accepted authority/evidence remain diagnostic only until full completion.
}
function permitted(s: ClientModelState, provider: string, id: string, now: bigint | null): boolean {
	const a = s.acceptedAuthority, applied = s.appliedEffects;
	return !s.retired && provider === CODEX_PROVIDER_ID && s.authorityStatus === "active" && !s.authoritativeRefreshRequired && s.leaseDeadline !== null && (now === null || (instant(now) && now < s.leaseDeadline)) && applied.status === "applied" && applied.target?.targetKind === "active_catalog" && equal(applied.target, s.desiredTarget) && applied.target.safetyGeneration === s.highestSeenGeneration && applied.target.inputFingerprint === a?.inputFingerprint && applied.target.authorityDigest === digest("authority", { outcome: "catalog", authority: a }) && applied.target.catalogDigest === digest("catalog", s.orderedModels) && !!a?.allowedCodexModelIds.includes(id) && s.selectableModelIds.includes(id);
}
function validEvent(e: unknown): e is ClientEvent {
	if (!object(e) || typeof e.type !== "string") return false;
	const keys: Record<string, string[]> = {
		observeDecision: ["bundle", "requestStartedMonoNs", "responseReceivedMonoNs", "config", "sourceRequestId", "source", "requestControllerInstanceId", "requestBranchContextEpoch"],
		leaseTick: [], localModelSelected: ["providerId", "modelId", "source", "ownEffectToken"], activeBranchRestored: ["reason", "branchSnapshot"],
		piEffectsSucceeded: ["effectPlanToken"], piEffectsFailed: ["effectPlanToken", "stage", "boundedErrorClass"], retryPiEffects: ["expectedTargetKey"], controllerShutdown: [],
	};
	if (!keys[e.type] || !closed(e, ["type", ...keys[e.type]])) return false;
	if (e.type === "observeDecision") return ["readback", "admission_header"].includes(e.source) && typeof e.sourceRequestId === "string" && typeof e.requestControllerInstanceId === "string" && typeof e.requestBranchContextEpoch === "bigint";
	if (e.type === "localModelSelected") return boundedString(e.providerId) && boundedString(e.modelId, 128) && ["set", "cycle", "restore"].includes(e.source);
	if (e.type === "activeBranchRestored") return ["startup", "reload", "new", "resume", "fork", "tree"].includes(e.reason);
	if (e.type === "piEffectsFailed") return ["register_provider", "resolve_model", "set_model", "adapter_invariant"].includes(e.stage) && boundedString(e.boundedErrorClass);
	return true;
}
function staleCompletion(s: ClientModelState, e: unknown): boolean {
	return object(e) && ["piEffectsSucceeded", "piEffectsFailed"].includes(e.type) && object(e.effectPlanToken) && !equal(e.effectPlanToken, s.pendingEffectPlan);
}
function protocolFailure(s: ClientModelState): Transition {
	closeApplication(s, true);
	return { state: s, piIntent: null, effects: [{ type: "diagnostic", stage: "adapter_invariant", boundedErrorClass: "effect_plan_protocol_error" }, { type: "requestReadback" }] };
}
export function deriveTransition(previous: ClientModelState, event: ClientEvent, now: bigint): Transition {
	if (previous.retired) return { state: previous, piIntent: null, effects: [] };
	const s = structuredClone(previous), effects: DecisionEffect[] = [];
	if (!validEvent(event) && !staleCompletion(s, event)) return protocolFailure(s);
	let force = false, suppress = false, retryObservationEligible = false, checkpoint: "safety_commit" | "preference_commit" | "branch_rebase" | "shutdown" | null = null;
	const requestReadback = () => { if (!effects.some(e => e.type === "requestReadback")) effects.push({ type: "requestReadback" }); };
	// Expiry is checked at commit, not inferred from timer delivery.
	if (s.leaseDeadline !== null && (s.authorityStatus === "active" || s.authorityStatus === "authoritative_empty") && (!instant(now) || now >= s.leaseDeadline)) {
		s.authorityStatus = "expired"; closeApplication(s); force = true; checkpoint = "safety_commit"; requestReadback();
	}
	switch (event.type) {
		case "activeBranchRestored": {
			const restored = restoreBranch(event.branchSnapshot, s.config);
			Object.assign(s, event.reason === "tree" ? joinSafety(safetyOf(s), restored.safety, false) : restored.safety);
			s.preRestrictionModelId = restored.preference; s.branchContextEpoch += 1n; s.restored = true; s.freshReadback = false;
			s.leaseDeadline = null; s.authoritativeRefreshRequired = s.authorityStatus === "legacy_restrictive_pending";
			s.restoreMemoryOnSuccess = false; closeApplication(s); force = true;
			checkpoint = event.reason === "tree" ? "branch_rebase" : "safety_commit"; requestReadback(); break;
		}
		case "observeDecision": {
			if (event.requestControllerInstanceId !== s.controllerInstanceId || event.requestBranchContextEpoch !== s.branchContextEpoch || !s.restored) break;
			if (event.source === "admission_header") {
				const h = parseHeaderInput(event.bundle, s.compatibilityModels, s.config);
				if (h.refresh) requestReadback();
				if (h.floor && s.highestSeenGeneration && compareGeneration(h.floor, s.highestSeenGeneration) < 0) break;
				if (h.decision) acceptFull(s, previous, h.decision, event, now);
				else if (h.legacy) acceptLegacy(s, previous, h.legacy);
				else if (h.close) {
					if (h.floor) quarantineState(s, h.floor);
					s.authoritativeRefreshRequired = true; closeApplication(s);
				}
			} else acceptFull(s, previous, event.bundle, event, now);
			retryObservationEligible = event.source === "readback" && object(event.bundle) && event.bundle.generation === s.highestSeenGeneration && parseDecision(event.bundle, s.compatibilityModels, event.config).ok;
			checkpoint = "safety_commit";
			break;
		}
		case "localModelSelected": {
			const own = event.ownEffectToken !== null && equal(event.ownEffectToken, s.pendingEffectPlan) && event.providerId === CODEX_PROVIDER_ID && event.modelId === s.desiredTarget.selectionModelId;
			s.currentModel = { provider: event.providerId, id: event.modelId };
			if (own) break;
			if (permitted(s, event.providerId, event.modelId, now)) break;
			// Pi notifies after selection, so correction is a new serialized attempt.
			closeApplication(s); force = true; break;
		}
		case "piEffectsSucceeded": {
			if (!s.pendingEffectPlan || !equal(event.effectPlanToken, s.pendingEffectPlan) || !equal(s.pendingEffectPlan.target, s.desiredTarget)) break;
			const target = s.pendingEffectPlan.target;
			s.pendingEffectPlan = null;
			if (target.targetKind === "active_catalog" && s.authorityStatus === "active" && !s.authoritativeRefreshRequired && s.leaseDeadline !== null && instant(now) && now < s.leaseDeadline) {
				s.appliedEffects = { status: "applied", target }; s.selectableModelIds = s.orderedModels.map(m => m.id);
				if (s.restoreMemoryOnSuccess) { s.preRestrictionModelId = null; s.restoreMemoryOnSuccess = false; checkpoint = "preference_commit"; }
			} else { s.appliedEffects = { status: "closed", target }; s.selectableModelIds = []; }
			suppress = true; break;
		}
		case "piEffectsFailed": {
			if (!s.pendingEffectPlan || !equal(event.effectPlanToken, s.pendingEffectPlan)) break;
			closeApplication(s, true); suppress = true;
			effects.push({ type: "diagnostic", stage: event.stage, boundedErrorClass: event.boundedErrorClass }); requestReadback(); break;
		}
		case "retryPiEffects": {
			if (equal(event.expectedTargetKey, s.desiredTarget) && s.appliedEffects.status !== "applying" && s.appliedEffects.status !== "applied") force = true;
			break;
		}
		case "controllerShutdown": s.retired = true; closeApplication(s); s.leaseDeadline = null; checkpoint = "shutdown"; suppress = true; break;
		case "leaseTick": break;
	}
	const intent = targetFor(s);
	const changed = !equal(intent.target, previous.desiredTarget);
	if (changed) closeApplication(s);
	s.desiredTarget = intent.target; s.orderedModels = intent.orderedModels;
	const matchingPending = s.pendingEffectPlan && equal(s.pendingEffectPlan.target, intent.target);
	const synchronized = s.appliedEffects.target && equal(s.appliedEffects.target, intent.target) && (s.appliedEffects.status === "applied" || s.appliedEffects.status === "closed");
	const retryObservation = retryObservationEligible && !matchingPending && !synchronized && s.appliedEffects.status === "effect_failed_closed";
	const piIntent = !s.retired && !suppress && (force || changed || retryObservation) && !matchingPending ? intent : null;
	if (piIntent) closeApplication(s);
	if (checkpoint && (!equal(safetyOf(s), safetyOf(previous)) || s.preRestrictionModelId !== previous.preRestrictionModelId || event.type === "activeBranchRestored" || event.type === "controllerShutdown")) effects.push({ type: "checkpoint", writeReason: checkpoint });
	return { state: s, piIntent, effects };
}
export function reduce(previous: ClientModelState, envelope: ControllerCommit): { state: ClientModelState; effects: DecisionEffect[] } {
	// Recognizably stale headers win even when the stale body/allocation is malformed.
	if (object(envelope) && ((Object.hasOwn(envelope, "controllerInstanceId") && envelope.controllerInstanceId !== previous.controllerInstanceId) || (Object.hasOwn(envelope, "baseStateRevision") && envelope.baseStateRevision !== previous.stateRevision) || (Object.hasOwn(envelope, "branchContextEpoch") && envelope.branchContextEpoch !== previous.branchContextEpoch))) return { state: previous, effects: [] };
	if (previous.retired) return { state: previous, effects: [] };
	const valid = closed(envelope, ["protocolVersion", "controllerInstanceId", "baseStateRevision", "branchContextEpoch", "commitMonoNs", "event"], ["allocation"]) && envelope.protocolVersion === 1 && typeof envelope.commitMonoNs === "bigint" && (validEvent(envelope.event) || staleCompletion(previous, envelope.event));
	let derived = valid ? deriveTransition(previous, envelope.event, envelope.commitMonoNs) : protocolFailure(structuredClone(previous));
	let s = derived.state;
	const next = previous.lastAllocatedOrdinal + 1n;
	const expected = derived.piIntent ? { kind: "apply_pi_catalog", token: { target: derived.piIntent.target, controllerInstanceId: previous.controllerInstanceId, planOrdinal: next, attemptOrdinal: next } } : { kind: "none" };
	if (!valid || !equal(envelope.allocation, expected)) {
		// Safety from the event survives a broken token channel; preference is not consumed.
		s.preRestrictionModelId = s.preRestrictionModelId ?? previous.preRestrictionModelId;
		derived = protocolFailure(s);
	} else if (derived.piIntent && envelope.allocation.kind === "apply_pi_catalog") {
		const token = structuredClone(envelope.allocation.token);
		closeApplication(s); s.pendingEffectPlan = token; s.lastAllocatedOrdinal = next; s.appliedEffects = { status: "applying" };
		derived.effects.push({ type: "applyPiCatalog", token, orderedModels: structuredClone(derived.piIntent.orderedModels), selectionModelId: derived.piIntent.selectionModelId });
	}
	s.stateRevision = previous.stateRevision + 1n;
	return freeze({ state: s, effects: derived.effects });
}

// ---- Serialized Pi adapter --------------------------------------------------------
interface ModelDecisionOptions {
	bootstrap: Bootstrap;
	compatibilityModels?: Model<any>[];
	nowMonoNs?: () => bigint;
	observe?: (record: Record<string, unknown>) => void;
	beforeWorker?: () => Promise<void> | undefined;
	beforeDequeue?: (event: ClientEvent) => Promise<void> | undefined;
}
interface RequestContext { requestStartedMonoNs: bigint; controllerInstanceId: string; branchContextEpoch: bigint; sourceRequestId: string }
const modelControllers = new WeakMap<ExtensionAPI, ModelDecisionController>();
const issuedControllerIds = new Set<string>();
function newControllerId(): string { let id: string; do { id = randomUUID(); } while (issuedControllerIds.has(id)); issuedControllerIds.add(id); return id; }
export function getModelDecisionController(pi: ExtensionAPI): ModelDecisionController {
	const controller = modelControllers.get(pi);
	if (!controller) throw new Error("Model decision controller is unavailable");
	return controller;
}
class ModelDecisionController {
	private state: ClientModelState;
	private ctx: ExtensionContext | undefined;
	private boundSessionId: string | undefined;
	private readonly now: () => bigint;
	private readonly events: Array<{ event: ClientEvent; resolve: () => void }> = [];
	private readonly work: Extract<DecisionEffect, { type: "applyPiCatalog" }>[] = [];
	private committing = false;
	private working = false;
	private ownSelection: EffectToken | null = null;
	private idleWaiters: Array<() => void> = [];
	private pollTimer: ReturnType<typeof setInterval> | undefined;
	private expiryTimer: ReturnType<typeof setTimeout> | undefined;
	private readonly requests = new Set<AbortController>();
	private ordinaryReadback: Promise<void> | undefined;
	private requestOrdinal = 0n;
	constructor(private readonly pi: ExtensionAPI, private readonly bootstrap: Bootstrap | undefined, models: Model<any>[], private readonly options?: ModelDecisionOptions) {
		this.now = options?.nowMonoNs ?? (() => process.hrtime.bigint());
		this.state = newClientModelState({ controllerInstanceId: newControllerId(), compatibilityModels: models, config: bootstrap?.modelDecision });
		pi.on("session_start", async (event, ctx) => {
			this.ctx = ctx;
			await this.restore(event.reason);
			const config = this.state.config;
			if (config && !this.state.retired) {
				this.pollTimer = setInterval(() => { void this.dispatch({ type: "leaseTick" }); void this.requestReadback(); }, Number(config.pollIntervalSeconds) * 1000);
				this.pollTimer.unref?.();
			}
		});
		pi.on("session_tree", async (_event, ctx) => { this.ctx = ctx; await this.restore("tree"); });
		pi.on("model_select", async (event, ctx) => {
			this.ctx = ctx;
			const token = this.ownSelection;
			const own = token && event.source === "set" && event.model.provider === CODEX_PROVIDER_ID && event.model.id === token.target.selectionModelId ? token : null;
			await this.dispatch({ type: "localModelSelected", providerId: event.model.provider, modelId: event.model.id, source: event.source, ownEffectToken: own });
		});
		pi.on("session_shutdown", async () => {
			if (this.state.retired) return;
			await this.dispatch({ type: "controllerShutdown" });
			clearInterval(this.pollTimer); clearTimeout(this.expiryTimer);
			for (const request of this.requests) request.abort();
			this.work.length = 0;
		});
	}
	snapshot(): ClientModelState { return this.state; }
	isSelectable(provider: string, id: string): boolean { return permitted(this.state, provider, id, this.now()); }
	requestContext(): RequestContext {
		return { requestStartedMonoNs: this.now(), controllerInstanceId: this.state.controllerInstanceId, branchContextEpoch: this.state.branchContextEpoch, sourceRequestId: String(++this.requestOrdinal) };
	}
	receiveHeaders(bundle: HeaderSnapshot, request: RequestContext): Promise<void> {
		return this.dispatch({ type: "observeDecision", bundle, requestStartedMonoNs: request.requestStartedMonoNs, responseReceivedMonoNs: this.now(), config: this.state.config,
			sourceRequestId: request.sourceRequestId, source: "admission_header", requestControllerInstanceId: request.controllerInstanceId, requestBranchContextEpoch: request.branchContextEpoch });
	}
	retryPiEffects(expectedTargetKey: EffectTarget): Promise<void> { return this.dispatch({ type: "retryPiEffects", expectedTargetKey }); }
	private restore(reason: string): Promise<void> {
		const sm = this.ctx!.sessionManager;
		const branchSnapshot = { sessionId: sm.getSessionId(), leafId: sm.getLeafId(), entries: structuredClone(sm.getBranch()) };
		this.boundSessionId = branchSnapshot.sessionId;
		return this.dispatch({ type: "activeBranchRestored", reason, branchSnapshot });
	}
	private trace(record: Record<string, unknown>): void { this.options?.observe?.(freeze(record)); }
	private dispatch(input: ClientEvent): Promise<void> {
		if (this.state.retired) return Promise.resolve();
		const event = freeze(structuredClone(input));
		this.trace({ kind: "event-ingress", event });
		return new Promise(resolve => { this.events.push({ event, resolve }); void this.drainEvents(); });
	}
	private async drainEvents(): Promise<void> {
		if (this.committing) return;
		this.committing = true;
		try {
			while (this.events.length) {
				const queued = this.events.shift()!;
				const barrier = this.options?.beforeDequeue?.(queued.event);
				if (barrier) await barrier;
				if (!this.state.retired) this.commit(queued.event);
				queued.resolve();
			}
		} finally { this.committing = false; this.settleIdle(); }
	}
	private commit(event: ClientEvent): void {
		const before = this.state, now = this.now();
		// No await or external callback between preview, token allocation and reduction.
		const intent = deriveTransition(before, event, now).piIntent;
		const ordinal = before.lastAllocatedOrdinal + 1n;
		const envelope: ControllerCommit = freeze({ protocolVersion: 1, controllerInstanceId: before.controllerInstanceId, baseStateRevision: before.stateRevision, branchContextEpoch: before.branchContextEpoch, commitMonoNs: now, event,
			allocation: intent ? { kind: "apply_pi_catalog", token: { target: intent.target, controllerInstanceId: before.controllerInstanceId, planOrdinal: ordinal, attemptOrdinal: ordinal } } : { kind: "none" } });
		const result = reduce(before, envelope);
		this.trace({ kind: "commit", envelope });
		this.state = result.state; // The only authority publication in the adapter.
		this.trace({ kind: "publish", state: this.state });
		this.trace({ kind: "enqueue-effects", effects: result.effects });
		for (const effect of result.effects) if (effect.type === "applyPiCatalog") this.work.push(effect);
		for (const effect of result.effects) {
			if (effect.type === "checkpoint") this.persist(effect.writeReason);
			else if (effect.type === "diagnostic" && this.ctx?.hasUI) this.ctx.ui.notify("Void model synchronization is closed; refreshing authority.", "warning");
			else if (effect.type === "requestReadback") void this.fetchReadback();
		}
		this.scheduleExpiry();
		// Start only after publication/enqueue, never from the pure preview.
		void this.drainWork();
	}
	private async drainWork(): Promise<void> {
		if (this.working || !this.ctx) return;
		this.working = true;
		try {
			while (this.work.length && !this.state.retired) {
				const effect = this.work.shift()!;
				const barrier = this.options?.beforeWorker?.();
				if (barrier) await barrier;
				if (this.state.retired || !equal(effect.token, this.state.pendingEffectPlan)) continue;
				let stage = "register_provider";
				try {
					if (!this.bootstrap) throw new Error("bootstrap_unavailable");
					registerVoidCodex(this.pi, this.bootstrap, structuredClone(effect.orderedModels), this.bootstrap.providers.find(p => p.kind === "codex")?.relayProviderId, this);
					const selection = effect.selectionModelId;
					if (selection !== null && (this.ctx.model?.provider !== CODEX_PROVIDER_ID || this.ctx.model?.id !== selection)) {
						stage = "resolve_model";
						const model = this.ctx.modelRegistry.find(CODEX_PROVIDER_ID, selection);
						if (!model) throw new Error("model_unavailable");
						stage = "set_model";
						this.ownSelection = effect.token;
						try { const selected = await this.pi.setModel(model); if (selected !== true) throw new Error("model_selection_refused"); }
						finally { this.ownSelection = null; }
					}
					await this.dispatch({ type: "piEffectsSucceeded", effectPlanToken: effect.token });
				} catch {
					// Do not serialize exception messages: native failures may contain credentials.
					await this.dispatch({ type: "piEffectsFailed", effectPlanToken: effect.token, stage, boundedErrorClass: "pi_effect_failed" });
				}
			}
		} finally { this.working = false; this.settleIdle(); }
	}
	private persist(writeReason: "safety_commit" | "preference_commit" | "branch_rebase" | "shutdown"): void {
		const sm = this.ctx?.sessionManager;
		// Pi may replace the SessionManager's header/path before retiring the old runtime.
		// Never rebind that runtime's checkpoint to the newly forked or empty session.
		if (!sm || sm.getSessionId() !== this.boundSessionId) return;
		const payload = { schemaVersion: 1, branchProof: { sessionId: sm.getSessionId(), parentEntryId: sm.getLeafId(), writeReason }, safety: safetyOf(this.state), preference: { preRestrictionModelId: this.state.preRestrictionModelId } };
		this.pi.appendEntry(STATE_ENTRY, structuredClone(payload));
		const appended = sm.getLeafEntry();
		if (!appended || appended.parentId !== payload.branchProof.parentEntryId) throw new Error("Model checkpoint parent mismatch");
	}
	private scheduleExpiry(): void {
		clearTimeout(this.expiryTimer); this.expiryTimer = undefined;
		if (this.state.retired || this.state.leaseDeadline === null || !["active", "authoritative_empty"].includes(this.state.authorityStatus)) return;
		const remaining = this.state.leaseDeadline - this.now();
		const ms = remaining <= 0n ? 0 : Number((remaining + 999999n) / 1000000n);
		this.expiryTimer = setTimeout(() => { void this.dispatch({ type: "leaseTick" }); }, Math.min(ms, 2147483647));
		this.expiryTimer.unref?.();
	}
	requestReadback(): Promise<void> {
		if (this.ordinaryReadback) return this.ordinaryReadback;
		const request = this.fetchReadback();
		this.ordinaryReadback = request;
		void request.finally(() => { if (this.ordinaryReadback === request) this.ordinaryReadback = undefined; });
		return request;
	}
	private async fetchReadback(): Promise<void> {
		const config = this.state.config;
		if (this.state.retired || !this.state.restored || !config || !this.bootstrap) return;
		const request = this.requestContext(), abort = new AbortController();
		this.requests.add(abort);
		this.trace({ kind: "readback-request", cache: "no-store", url: config.readbackUrl, controllerInstanceId: request.controllerInstanceId, branchContextEpoch: request.branchContextEpoch });
		try {
			const response = await fetch(config.readbackUrl, { method: "GET", cache: "no-store", headers: { authorization: "Bearer " + this.bootstrap.authToken }, signal: abort.signal });
			const received = this.now();
			if (!response.ok) { await response.body?.cancel(); return; }
			// Bound before decoding JSON, including chunked bodies with no content-length.
			if (!response.body) return;
			const reader = response.body.getReader(); let size = 0; const chunks: Uint8Array[] = [];
			try {
				for (;;) { const part = await reader.read(); if (part.done) break; size += part.value.byteLength; if (size > 16384) { await reader.cancel(); return; } chunks.push(part.value); }
			} finally { reader.releaseLock(); }
			const bundle: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks)));
			await this.dispatch({ type: "observeDecision", bundle, requestStartedMonoNs: request.requestStartedMonoNs, responseReceivedMonoNs: received, config,
				sourceRequestId: request.sourceRequestId, source: "readback", requestControllerInstanceId: request.controllerInstanceId, requestBranchContextEpoch: request.branchContextEpoch });
		} catch {
			// A failed readback is not authority and cannot renew the existing lease.
			// The configured lifecycle will retry; shutdown aborts are intentionally inert.
		} finally { this.requests.delete(abort); }
	}
	whenIdle(): Promise<void> {
		// Network reads are deliberately excluded: a closed controller can be inspected
		// while the server is silent. Pi work and committed event work must settle.
		return !this.committing && !this.working && this.events.length === 0 && this.work.length === 0 ? Promise.resolve() : new Promise(resolve => this.idleWaiters.push(resolve));
	}
	private settleIdle(): void {
		if (!this.committing && !this.working && !this.events.length && !this.work.length) for (const resolve of this.idleWaiters.splice(0)) resolve();
	}
}
