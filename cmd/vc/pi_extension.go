package main

const piVoidCodexExtensionSource = `// void-code-managed-pi-extension:v1
import { execFileSync, spawn as nativeSpawn } from "node:child_process";
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
import { clampThinkingLevel, createAssistantMessageEventStream, getCurrentSystemPrompt, getCurrentTools, normalizeContext } from "@earendil-works/pi-ai";
import { CustomEditor, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { isKeyRelease, matchesKey } from "@earendil-works/pi-tui";

const CODEX_PROVIDER_ID = "void-codex";
const CODEX_MODEL_ID = "gpt-6.1-sol";

interface BootstrapModel {
	id: string;
	label?: string;
	default?: boolean;
}
interface BootstrapProvider {
	kind: "codex";
	relayProviderId: string;
	models: string[];
	// The catalog's labels, same order as models (default first). vc builds both from Keys'
	// catalog (void-works#81), so the picker needs no extension change for a new model.
	modelInfo?: BootstrapModel[];
}
interface Bootstrap {
	version: number;
	relayUrl: string;
	authToken: string;
	providers: BootstrapProvider[];
}
let activeBootstrap: Bootstrap | undefined;
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
}


export default function (pi: ExtensionAPI, options?: ClipboardExtensionOptions) {
	registerDesktopLifecycle(pi);
	registerLaunchNotice(pi);
	registerBillingRefusalReply(pi);
	registerFullscreenClipboardLifecycle(pi, options?.clipboardIO);
	registerPromptKeys(pi);
	const bootstrap = loadBootstrap();
	if (!bootstrap) return;
	activeBootstrap = bootstrap;
	let managedSearchAvailable = false;
	let hasCodexGrant = false;
	for (const provider of bootstrap.providers) {
		if (provider.kind === "codex") {
			hasCodexGrant = true;
			const labels = new Map((provider.modelInfo ?? []).map((m) => [m.id, m.label ?? ""]));
			const ids = provider.models.filter((id, index) => typeof id === "string" && id.trim() !== "" && provider.models.indexOf(id) === index);
			const models = ids.map((id) => codexModel(id, codexName(id, labels.get(id))));
			if (models.length === 0) continue;
			registerVoidCodex(pi, bootstrap, models, provider.relayProviderId);
			managedSearchAvailable = true;
		}
	}
	if (!hasCodexGrant) {
		registerVoidCodex(pi, bootstrap, [codexModel(CODEX_MODEL_ID, codexName(CODEX_MODEL_ID))]);
	}
	if (managedSearchAvailable) {
		pi.on("before_agent_start", async (event) => ({
			systemPrompt: event.systemPrompt + "\n\n" + MANAGED_WEB_SEARCH_INSTRUCTION,
		}));
	}
}

function registerVoidCodex(
	pi: ExtensionAPI,
	bootstrap: Bootstrap,
	models: Model<any>[],
	relayProviderId?: string,
): void {
	pi.registerProvider(CODEX_PROVIDER_ID, {
		name: "Void ChatGPT relay",
		baseUrl: bootstrap.relayUrl,
		apiKey: bootstrap.authToken,
		api: "void-codex-sse",
		...(relayProviderId ? { headers: { "x-void-provider": relayProviderId } } : {}),
		models,
		streamSimple: streamVoidCodex,
	});
}

// Esc clears the prompt, Up/Down recall prompt history (void-works#77), with Pi 0.87.1's public API
// only: the main editor becomes a CustomEditor subclass through ctx.ui.setEditorComponent, which is
// how Pi documents changing editor keys. Pi wires its own handlers, submit, autocomplete and prompt
// history into it, and puts its default editor back on /new, /resume, fork and /reload.
//
// Esc runs Pi's handler first (abort a turn, cancel !bash, leave bash mode, double-Esc /tree, a
// retry's or compaction's own cancel). Only when the agent was idle, nothing was queued and that
// handler left a non-empty draft as it was does the draft clear, with setText, so Pi's undo brings
// it back. Autocomplete keeps its own Esc.
//
// Up/Down run Pi's own tui.editor.historyPrevious/historyNext actions, which browse history from
// anywhere in the draft. Those actions have no default key; the bare arrow is added to them for this
// one keystroke through the KeybindingsManager Pi hands the factory, so keybindings.json is never
// written, /hotkeys and /reload keep what the person set, and other editors (dialogs) keep arrows
// that move the cursor. Autocomplete keeps its own Up/Down, and Pi drops key releases before input.
function registerPromptKeys(pi: ExtensionAPI): void {
	// Built here, not at module scope: a host without CustomEditor keeps Pi's editor instead of
	// failing the whole extension (and the transport with it) on load.
	if (typeof CustomEditor !== "function") return;
	class PromptKeysEditor extends CustomEditor {
		private readonly keys: any;
		private readonly idle: () => boolean;

		constructor(tui: any, theme: any, keybindings: any, idle: () => boolean) {
			super(tui, theme, keybindings);
			this.keys = keybindings;
			this.idle = idle;
		}

		handleInput(data: string): void {
			if (this.keys.matches(data, "app.interrupt") && !this.isShowingAutocomplete()) {
				const draft = this.getText();
				const idle = this.idle();
				super.handleInput(data);
				if (idle && draft.length > 0 && this.getText() === draft) this.setText("");
				return;
			}
			const key = matchesKey(data, "up") ? "up" : matchesKey(data, "down") ? "down" : undefined;
			if (!key || this.isShowingAutocomplete()) {
				super.handleInput(data);
				return;
			}
			const action = key === "up" ? "tui.editor.historyPrevious" : "tui.editor.historyNext";
			const bindings = this.keys.getUserBindings();
			this.keys.setUserBindings({ ...bindings, [action]: [...this.keys.getKeys(action), key] });
			try {
				super.handleInput(data);
			} finally {
				this.keys.setUserBindings(bindings);
			}
		}
	}
	pi.on("session_start", async (_event, ctx) => {
		if (ctx.mode !== "tui" || !ctx.hasUI) return;
		const idle = (): boolean => ctx.isIdle() && !ctx.hasPendingMessages();
		ctx.ui.setEditorComponent((tui, theme, keybindings) => new PromptKeysEditor(tui, theme, keybindings, idle));
	});
}

// Pi does not expose fullscreen selection text to extensions. This adapter is deliberately
// The 0.84 adapter extracts the OSC52 output on a disposable receiver. Pi 0.87
// exposes getActiveSelectionText(): use that snapshot without emitting OSC52.
// Neither path replaces the live terminal. Unknown Pi versions fail passive.
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
	if (!authority || (options.piVersion !== "0.84.1" && options.piVersion !== "0.87.1")) {
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
	const nativeTextCopy = options.piVersion === "0.87.1";
	if (nativeTextCopy && (typeof tui.getActiveSelectionText !== "function" || typeof tui.copyActiveSelectionToClipboard !== "function")) return failPassive();
	const originalActiveCopy = tui.copyActiveSelectionToClipboard;
	const originalSelectionMouse = tui.handleSelectionMouseEvent;
	const originalViewportInput = tui.handleViewportInput;
	const originalSetFocus = tui.setFocus;
	const originalShowOverlay = tui.showOverlay;
	const inheritedHooks: Array<[string, any]> = [
		["copySelectionToClipboard", originalCopy], ...(nativeTextCopy ? [["copyActiveSelectionToClipboard", originalActiveCopy] as [string, any]] : []),
		["handleSelectionMouseEvent", originalSelectionMouse],
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
		if (nativeTextCopy) {
			// 0.87 exposes the exact selected text. Do not invoke its async
			// clipboard callback: it can flash before our queued native write.
			const text = tui.getActiveSelectionText();
			if (typeof text === "string") admit(text);
			return;
		}
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
	// 0.87's Ctrl+X prefers the selected transcript only when copy-on-select
	// is disabled. That path calls this method directly, not the mouse copier.
	// Keep the pre-existing Ctrl+X last-assistant-message path untouched.
	const managedActiveCopy = function (this: any): void {
		managedCopy.call(tui);
	};
	if (nativeTextCopy) tui.copyActiveSelectionToClipboard = managedActiveCopy;
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
		if (nativeTextCopy && tui.copyActiveSelectionToClipboard === managedActiveCopy) tui.copyActiveSelectionToClipboard = originalActiveCopy;
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
		// A cold Windows PowerShell start can take well over 5 s (#206), so Windows gets 15 s.
		timer = setTimeout(abort, options.platform === "win32" ? 15000 : 5000);
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

// vc decides at launch, from the wallet its own /v1/vc/me reported, whether the person needs a word
// about money (low balance, or a day Relay will refuse), and hands it over as VC_LAUNCH_NOTICE. It is
// shown here, not printed by vc: Pi's fullscreen mode clears whatever was on the terminal before it.
//
// Docked above the editor as a widget, not notify()'d into the transcript: a reopened chat
// (--session <file>) appends its whole history after session_start, which would push a transcript
// warning off screen. It stays until the first prompt — by then it has been read — and the first
// before_agent_start takes it down.
//
// Only on session_start with reason "startup": the notice belongs to the launch. Pi runs this factory
// again for /new, /resume, fork and /reload, with VC_LAUNCH_NOTICE still in the environment, and a
// later session would show a notice about a wallet that may have changed since.
const LAUNCH_NOTICE_WIDGET_KEY = "void-code-launch-notice";

function registerLaunchNotice(pi: ExtensionAPI): void {
	const notice = process.env.VC_LAUNCH_NOTICE;
	if (!notice) return;
	let docked = false;
	pi.on("session_start", async (event, ctx) => {
		if (event.reason !== "startup" || !ctx.hasUI) return;
		// One line per notice: the wallet's and the weekly limit's can both be set (vc's launchNotice).
		ctx.ui.setWidget(LAUNCH_NOTICE_WIDGET_KEY, notice.split("\n").map((line) => ctx.ui.theme.fg("warning", line)), { placement: "aboveEditor" });
		docked = true;
	});
	pi.on("before_agent_start", async (_event, ctx) => {
		if (!docked) return;
		docked = false;
		ctx.ui.setWidget(LAUNCH_NOTICE_WIDGET_KEY, undefined);
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
		if (value.version !== 1 || !value.relayUrl || !value.authToken || !Array.isArray(value.providers)) throw new Error("invalid bootstrap response");
		return value;
	} catch (error) {
		console.error("void-code: managed Pi provider unavailable; run vc login, then vc (" + (error instanceof Error ? error.message : String(error)) + ")");
		return undefined;
	}
}

function codexName(id: string, label?: string): string {
	if (label && label.trim() !== "") return label.trim() + " via Void relay";
	// Fallback labels for a vc bootstrap without the catalog's.
	if (id === "gpt-6.1-sol") return "GPT-6.1 Sol via Void relay";
	if (id === "gpt-5.6-terra") return "GPT-5.6 Terra via Void relay";
	if (id === "gpt-6-luna") return "GPT-6 Luna via Void relay";
	if (id === "gpt-6-astra") return "GPT-6 Astra via Void relay";
	return id + " via Void relay";
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

function streamVoidCodex(
	model: Model<any>,
	context: Context,
	options?: SimpleStreamOptions,
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
			if (!activeBootstrap) throw new Error("Void provider bootstrap is unavailable");
			const relayURL = activeBootstrap.relayUrl.replace(/\/+$/, "") + "/codex/responses";
			const token = activeBootstrap.authToken;
			const providerID = activeBootstrap.providers.find((provider) => provider.kind === "codex")?.relayProviderId;
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

			const response = await fetch(relayURL, {
				method: "POST",
				headers,
				body,
				signal: options?.signal,
			});
			await options?.onResponse?.({ status: response.status, headers: headersToRecord(response.headers) }, model);
			if (!response.ok) {
				const text = await response.text();
				const refusal = billingRefusalSentence(response.status, text);
				if (refusal !== undefined) billingRefusals.add(refusal);
				throw new Error(relayFailureMessage(response.status, text));
			}
			if (!response.body) throw new Error("Void relay Codex response had no body");

			stream.push({ type: "start", partial: output });
			const { processResponsesStream } = await openAIResponsesShared();
			await processResponsesStream(parseSSE(response, options?.signal), output, stream, model);
			normalizeUsage(output);
			if (options?.signal?.aborted) throw new Error("Request was aborted");
			stream.push({ type: "done", reason: output.stopReason as "stop" | "length" | "toolUse", message: output });
			stream.end();
		} catch (error) {
			for (const block of output.content) delete (block as any).partialJson;
			output.stopReason = options?.signal?.aborted ? "aborted" : "error";
			output.errorMessage = error instanceof Error ? error.message : String(error);
			stream.push({ type: "error", reason: output.stopReason, error: output });
			stream.end();
		}
	})();

	return stream;
}

// What Pi shows the person when Relay answers non-2xx. Only Relay's 402 — the wallet's
// wallet_daily_charge_required, the percentage cap — is shown as the sentence it carries in
// error.message: no status line, no JSON. Every other status keeps the raw text even when its body
// has an error.message, because Relay passes upstream answers through with their status, and Pi's
// auto-retry reads this text: the "HTTP 5xx" prefix is what makes an upstream 5xx retryable. A 402
// that is not JSON, or lacks a non-empty string error.message, keeps the raw text too.
function relayFailureMessage(status: number, text: string): string {
	return billingRefusalSentence(status, text) ?? "Void relay Codex request failed: HTTP " + status + ": " + text;
}

// Relay's billing refusal — every 402 it sends (the unpaid week, the daily charge, the weekly
// limit) carries its sentence in error.message — or undefined for anything else.
function billingRefusalSentence(status: number, text: string): string | undefined {
	if (status !== 402) return undefined;
	try {
		const parsed: unknown = JSON.parse(text);
		if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
			const error = (parsed as { error?: unknown }).error;
			if (error && typeof error === "object" && !Array.isArray(error)) {
				const message = (error as { message?: unknown }).message;
				if (typeof message === "string" && message.trim() !== "") return message;
			}
		}
	} catch {}
	return undefined;
}

// Billing refusals (void-board#373). Pi 0.87 follows an errored reply with «If this looks like a pi
// bug, /bug sends a report to the developers.» — once per process, so in the 09-29 e2e the unpaid
// refusal got it and the limit refusal, later in the same Pi, did not. A refusal to pay is not a bug,
// and Pi skips that line only for a reply that did not end in an error, so in the terminal UI the
// refusal ends the turn as a plain reply carrying the same sentence. Only Relay's 402 sentences
// qualify: streamVoidCodex records each one it throws, and message_end turns exactly those back.
// The reply is marked, and buildCodexBody leaves marked replies out of what the model sees — as Pi
// leaves out an errored one. Other modes (the desktop's RPC, print) keep the error as it was.
const billingRefusals = new Set<string>();
const BILLING_REFUSAL_MARK = "voidBillingRefusal";

function billingRefusalReply(message: any, mode: string | undefined): any {
	if (mode !== "tui" || !message || message.role !== "assistant" || message.stopReason !== "error") return undefined;
	if (typeof message.errorMessage !== "string" || !billingRefusals.has(message.errorMessage)) return undefined;
	const { errorMessage, ...rest } = message;
	return { ...rest, content: [{ type: "text", text: errorMessage }], stopReason: "stop", [BILLING_REFUSAL_MARK]: true };
}

function registerBillingRefusalReply(pi: ExtensionAPI): void {
	pi.on("message_end", async (event, ctx) => {
		const message = billingRefusalReply(event.message, ctx.mode);
		return message ? { message } : undefined;
	});
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
	// agent-core >=0.86 sends only a transcript. Normalize the legacy shorthand if
	// present, then replay system deltas rather than reading obsolete top-level fields.
	const normalized = normalizeContext(context);
	const transcript = { ...normalized, messages: normalized.messages.filter((message: any) => message?.[BILLING_REFUSAL_MARK] !== true) };
	const instructions = getCurrentSystemPrompt(transcript.messages);
	const tools = getCurrentTools(transcript.messages);
	const body: Record<string, unknown> = {
		model: model.id,
		store: false,
		stream: true,
		instructions: instructions || "You are a helpful assistant.",
		input: convertResponsesMessages(model, transcript, new Set(["openai", "openai-codex", "opencode"]), { includeSystemPrompt: false }),
		text: { verbosity: (options as any)?.textVerbosity || "low" },
		include: ["reasoning.encrypted_content"],
		prompt_cache_key: promptCacheKey(options?.sessionId),
		tool_choice: "auto",
		parallel_tool_calls: true,
	};
	if ((options as any)?.temperature !== undefined) body.temperature = (options as any).temperature;
	if ((options as any)?.serviceTier !== undefined) body.service_tier = (options as any).serviceTier;
	if (tools.length > 0) body.tools = convertResponsesTools(tools, { strict: null });
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
`
