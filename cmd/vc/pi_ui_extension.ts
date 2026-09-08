// void-code-managed-pi-ui-extension:v1
/**
 * Claude Code-style compact tool display.
 *
 * Collapsed mode keeps one mutable line for the current activity and writes a
 * separate short history line for every completed tool. Ctrl+O restores the
 * normal per-tool calls and full results for inspection.
 */

import type { ExtensionAPI, ExtensionContext, Theme } from "@earendil-works/pi-coding-agent";
import {
	createBashTool,
	createEditTool,
	createFindTool,
	createGrepTool,
	createLsTool,
	createReadTool,
	createWriteTool,
} from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";
import { homedir } from "os";

/**
 * Shorten a path by replacing home directory with ~
 */
function shortenPath(path: string): string {
	const home = homedir();
	if (path.startsWith(home)) {
		return `~${path.slice(home.length)}`;
	}
	return path;
}

// Cache for built-in tools by cwd
const toolCache = new Map<string, ReturnType<typeof createBuiltInTools>>();

function createBuiltInTools(cwd: string) {
	return {
		read: createReadTool(cwd),
		bash: createBashTool(cwd),
		edit: createEditTool(cwd),
		write: createWriteTool(cwd),
		find: createFindTool(cwd),
		grep: createGrepTool(cwd),
		ls: createLsTool(cwd),
	};
}

function getBuiltInTools(cwd: string) {
	let tools = toolCache.get(cwd);
	if (!tools) {
		tools = createBuiltInTools(cwd);
		toolCache.set(cwd, tools);
	}
	return tools;
}

const TOOL_GROUP_ENTRY = "compact-tool-group"; // Legacy entries from earlier sessions.
const TOOL_HISTORY_ENTRY = "compact-tool-history"; // Legacy per-tool entries.
const TOOL_HISTORY_BLOCK_ENTRY = "compact-tool-history-block";

interface CompactToolCall {
	id: string;
	name: string;
	args: unknown;
	description: string;
	activity: string;
	startedAt: number;
	done: boolean;
	isError: boolean;
}

interface CompactToolGroup {
	calls: CompactToolCall[];
}

interface CompactToolHistory {
	text: string;
	isError: boolean;
	durationMs?: number;
}

interface CompactToolHistoryBlock {
	items: CompactToolHistory[];
}

function asRecord(value: unknown): Record<string, unknown> {
	return value && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

function compactText(value: unknown, maxLength = 100): string {
	const text = String(value ?? "").replace(/\s+/g, " ").trim();
	return text.length <= maxLength ? text : `${text.slice(0, maxLength - 1)}…`;
}

function redactCommand(value: unknown): string {
	return String(value ?? "")
		.replace(/\b([A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD|AUTH)[A-Z0-9_]*)=(?:'[^']*'|"[^"]*"|[^\s]+)/gi, "$1=REDACTED")
		.replace(/((?:^|\s)--?(?:token|key|secret|password|authorization)(?:=|\s+))(?:'[^']*'|"[^"]*"|[^\s]+)/gi, "$1REDACTED")
		.replace(/(Bearer\s+)[A-Za-z0-9._~+\/-]+/gi, "$1REDACTED");
}

function describeReadTarget(rawArgs: unknown): string {
	const args = asRecord(rawArgs);
	const path = shortenPath(compactText(args.path, 90) || ".");
	const offset = typeof args.offset === "number" ? args.offset : undefined;
	const limit = typeof args.limit === "number" ? args.limit : undefined;
	if (offset === undefined && limit === undefined) return path;
	const start = offset ?? 1;
	const end = limit === undefined ? "" : `–${start + limit - 1}`;
	return `${path}:${start}${end}`;
}

function resultText(rawResult: unknown): string {
	const result = asRecord(rawResult);
	if (!Array.isArray(result.content)) return "";
	return result.content
		.map((item) => asRecord(item))
		.filter((item) => item.type === "text")
		.map((item) => String(item.text ?? ""))
		.join("\n");
}

function describeToolCall(name: string, rawArgs: unknown): string {
	const args = asRecord(rawArgs);
	if (name === "bash") return `$ ${compactText(redactCommand(args.command)) || "…"}`;
	if (name === "read") return `read ${describeReadTarget(args)}`;
	if (name === "edit" || name === "write") {
		return `${name} ${shortenPath(compactText(args.path) || "…")}`;
	}
	if (name === "grep") {
		return `grep /${compactText(args.pattern, 55)}/ in ${shortenPath(compactText(args.path) || ".")}`;
	}
	if (name === "find") {
		return `find ${compactText(args.pattern, 55) || "…"} in ${shortenPath(compactText(args.path) || ".")}`;
	}
	if (name === "ls") return `ls ${shortenPath(compactText(args.path) || ".")}`;
	return name;
}

function renderActiveToolCall(
	name: string,
	args: unknown,
	theme: Theme,
	context: { executionStarted: boolean; isPartial: boolean },
): Text {
	if (!context.executionStarted || !context.isPartial) return new Text("", 0, 0);
	return new Text(theme.fg("accent", `● ${describeToolCall(name, args)}`), 0, 0);
}

function legacyCompletedDescription(call: CompactToolCall): string {
	const detail = call.description.replace(new RegExp(`^${call.name}\\s+`), "");
	if (call.name === "read") return `Прочитал ${detail}`;
	if (call.name === "edit") return `Изменил ${detail}`;
	if (call.name === "write") return `Записал ${detail}`;
	if (call.name === "ls") return `Посмотрел содержимое ${detail}`;
	if (call.name === "bash") {
		return completedToolDescription("bash", { command: detail.replace(/^\$\s*/, "") }, call.isError);
	}
	if (call.name === "grep" || call.name === "find") return `Выполнил поиск: ${detail}`;
	return `Завершил ${call.description}`;
}

function formatDuration(durationMs: number | undefined): string {
	if (durationMs === undefined || !Number.isFinite(durationMs) || durationMs < 0) return "";
	if (durationMs < 1) return "<1 мс";
	if (durationMs < 1000) return `${Math.round(durationMs)} мс`;
	if (durationMs < 60_000) {
		const seconds = durationMs < 10_000 ? (durationMs / 1000).toFixed(1) : Math.round(durationMs / 1000).toString();
		return `${seconds.replace(".", ",")} с`;
	}
	const totalSeconds = Math.round(durationMs / 1000);
	const minutes = Math.floor(totalSeconds / 60);
	const seconds = totalSeconds % 60;
	return seconds > 0 ? `${minutes} мин ${seconds} с` : `${minutes} мин`;
}

function describeHttpRequest(rawCommand: unknown): string {
	const command = String(rawCommand ?? "");
	const urlMatch = command.match(/https?:\/\/[^\s'"`]+/i);
	if (!urlMatch) return "";
	let method = command.match(/(?:^|\s)(?:-X|--request)\s+([A-Z]+)/i)?.[1]?.toUpperCase();
	if (!method) method = /(?:^|\s)(?:-I|--head)(?:\s|$)/.test(command) ? "HEAD" : "GET";
	if (method === "GET" && /(?:^|\s)(?:-d|--data(?:-raw|-binary|-urlencode)?)(?:\s|=)/.test(command)) method = "POST";
	try {
		const url = new URL(urlMatch[0]);
		for (const key of [...url.searchParams.keys()]) {
			if (/token|key|secret|password|auth|signature/i.test(key)) url.searchParams.set(key, "REDACTED");
		}
		return `${method} ${url.pathname}${url.search}`;
	} catch {
		return `${method} ${compactText(urlMatch[0], 80)}`;
	}
}

function completedToolDescription(name: string, rawArgs: unknown, isError: boolean, rawResult?: unknown): string {
	const args = asRecord(rawArgs);
	const path = shortenPath(compactText(args.path, 90) || ".");
	const pattern = compactText(args.pattern, 55) || "…";
	const command = String(args.command ?? "").toLowerCase();
	const request = /curl|wget/.test(command) ? describeHttpRequest(args.command) : "";

	if (isError) {
		if (name === "read") return `Не удалось прочитать ${describeReadTarget(args)}`;
		if (name === "edit") return `Не удалось изменить ${path}`;
		if (name === "write") return `Не удалось записать ${path}`;
		if (name === "grep" || name === "find") return `Не удалось выполнить поиск «${pattern}» в ${path}`;
		if (name === "ls") return `Не удалось прочитать содержимое ${path}`;
		if (name === "bash" && request) return `Не удалось выполнить запрос ${request}`;
		if (name === "bash") return `Ошибка команды: ${compactText(redactCommand(args.command), 90) || "…"}`;
		if (name === "web_search") return "Не удалось выполнить поиск в интернете";
		if (name === "fetch_content" || name === "get_search_content") return "Не удалось прочитать источник";
		return `Ошибка при выполнении ${name}`;
	}

	if (name === "read") return `Прочитал ${describeReadTarget(args)}`;
	if (name === "edit") {
		const count = Array.isArray(args.edits) ? args.edits.length : 1;
		return `Изменил ${path}${count > 1 ? ` (${count} фрагм.)` : ""}`;
	}
	if (name === "write") {
		const lines = typeof args.content === "string" ? args.content.split("\n").length : undefined;
		return `Записал ${path}${lines ? ` (${lines} строк)` : ""}`;
	}
	if (name === "grep") {
		const glob = args.glob ? ` · ${compactText(args.glob, 35)}` : "";
		return `Проверил «${pattern}» в ${path}${glob}`;
	}
	if (name === "find") return `Искал «${pattern}» в ${path}`;
	if (name === "ls") return `Посмотрел содержимое ${path}`;
	if (name === "web_search") {
		const queries = Array.isArray(args.queries) ? args.queries : [args.query];
		const query = compactText(queries.filter(Boolean).join("; "), 90);
		return query ? `Поиск в интернете: «${query}»` : "Выполнил поиск в интернете";
	}
	if (name === "fetch_content") {
		const urls = Array.isArray(args.urls) ? args.urls : [args.url];
		const url = compactText(urls.filter(Boolean).join(", "), 90);
		return url ? `Изучил источник ${url}` : "Изучил найденный источник";
	}
	if (name === "get_search_content") return "Изучил полный текст найденного источника";
	if (name === "bash") {
		const original = compactText(redactCommand(args.command), 90) || "…";
		if (/tsc|typecheck|loadextensions|json\.tool/.test(command)) return "Проверил расширение Pi";
		if (/pytest|vitest|playwright|npm test|yarn test/.test(command)) return "Прогнал тесты";
		if (/ruff|mypy|eslint|prettier/.test(command)) return "Проверил качество кода";
		if (/git status/.test(command)) return "Проверил состояние Git";
		if (/git diff/.test(command)) return "Проверил изменения Git";
		if (/git log/.test(command)) return "Посмотрел историю Git";
		if (request) {
			const httpCode = resultText(rawResult).match(/HTTP\s+(\d{3})/i)?.[1];
			return `${request}${httpCode ? ` → HTTP ${httpCode}` : ""}`;
		}
		if (/docker|compose/.test(command)) return "Проверил Docker-сервисы";
		return `Выполнил: ${original}`;
	}
	return `Завершил ${name}`;
}

function statusAfterTool(name: string, rawArgs: unknown, isError: boolean, rawResult?: unknown): string {
	const args = asRecord(rawArgs);
	const path = shortenPath(compactText(args.path, 75) || ".");
	const pattern = compactText(args.pattern, 45) || "…";
	const command = String(args.command ?? "").toLowerCase();
	const request = /curl|wget/.test(command) ? describeHttpRequest(args.command) : "";

	if (isError) {
		if (name === "read") return `Разбираюсь с ошибкой чтения ${path}…`;
		if (name === "bash" && request) return `Разбираюсь с ошибкой запроса ${request}…`;
		return `Разбираюсь с ошибкой ${name}…`;
	}
	if (name === "read") return `Сопоставляю данные из ${path}…`;
	if (name === "edit" || name === "write") return `Проверяю изменения в ${path}…`;
	if (name === "grep" || name === "find") return `Проверяю найденное по «${pattern}»…`;
	if (name === "ls") return `Сверяю содержимое ${path}…`;
	if (name === "bash" && request) {
		const httpCode = resultText(rawResult).match(/HTTP\s+(\d{3})/i)?.[1];
		return `Изучаю ответ API: ${request}${httpCode ? ` → HTTP ${httpCode}` : ""}…`;
	}
	if (name === "bash" && /pytest|vitest|playwright|npm test|yarn test/.test(command)) {
		return "Сверяю результаты тестов…";
	}
	if (name === "bash" && /tsc|typecheck|loadextensions/.test(command)) return "Сверяю проверку расширения Pi…";
	if (name === "bash") return "Изучаю результат команды…";
	if (name === "web_search") return "Сопоставляю найденные источники…";
	if (name === "fetch_content" || name === "get_search_content") return "Сверяю данные источника…";
	return `Проверяю результат ${name}…`;
}

function statusFromPrompt(prompt: string): string {
	const text = prompt.toLowerCase();
	if (/скрин|screenshot|изображ|\.png|\.jpe?g/.test(text)) return "Изучаю скриншот…";
	if (/(^|\W)pi(\W|$)|tool.?call|thinking|working|вывод|интерфейс/.test(text)) {
		return "Разбираюсь с отображением Pi…";
	}
	if (/тест|test|pytest|vitest|playwright/.test(text)) return "Разбираюсь с тестами…";
	if (/ошиб|error|bug|баг|сломал|не работает/.test(text)) return "Ищу причину проблемы…";
	if (/файл|директор|проект|репозитор/.test(text)) return "Изучаю файлы проекта…";
	return "Разбираюсь с задачей…";
}

function statusForTool(name: string, rawArgs: unknown): string {
	const args = asRecord(rawArgs);
	const path = shortenPath(compactText(args.path, 75) || ".");
	if (name === "read") {
		if (/\.(png|jpe?g|gif|webp)$/i.test(path)) return "Изучаю скриншот…";
		if (/pi-coding-agent|\.pi\//.test(path)) return "Читаю файлы Pi…";
		return `Читаю ${path}…`;
	}
	if (name === "edit" || name === "write") {
		if (/pi-coding-agent|\.pi\//.test(path)) return "Меняю настройки отображения Pi…";
		return `${name === "edit" ? "Меняю" : "Записываю"} ${path}…`;
	}
	if (name === "grep" || name === "find") {
		const pattern = compactText(args.pattern, 55) || "…";
		if (/pi-coding-agent|\.pi\//.test(path)) return `Ищу «${pattern}» в файлах Pi…`;
		return `Ищу «${pattern}» в ${path}…`;
	}
	if (name === "ls") return `Читаю содержимое ${path}…`;
	if (name === "web_search") return "Ищу актуальную информацию в интернете…";
	if (name === "fetch_content" || name === "get_search_content") return "Изучаю найденный источник…";
	if (name === "bash") {
		const command = compactText(args.command, 300).toLowerCase();
		if (/tsc|typecheck|loadextensions|json\.tool/.test(command)) return "Проверяю, что расширение Pi работает…";
		if (/pytest|vitest|playwright|npm test|yarn test/.test(command)) return "Запускаю тесты…";
		if (/ruff|mypy|eslint|prettier/.test(command)) return "Проверяю качество кода…";
		if (/git (status|diff|log)/.test(command)) return "Проверяю изменения в Git…";
		if (/curl|wget/.test(command)) {
			const request = describeHttpRequest(args.command);
			return request ? `Проверяю API: ${request}…` : "Проверяю доступ к API…";
		}
		if (/docker|compose/.test(command)) return "Проверяю Docker-сервисы…";
		if (/(^|\s)(rg|grep|find|ls)(\s|$)/.test(command)) return "Ищу информацию в файлах…";
		return `Выполняю: ${compactText(redactCommand(args.command), 80) || "команду"}…`;
	}
	return `Выполняю ${name}…`;
}

function setWorkingStatus(ctx: ExtensionContext, status: string): void {
	ctx.ui.setWorkingMessage(status);
}

export default function (pi: ExtensionAPI) {
	if (process.env.VC_DESKTOP_SESSION !== "1") return;

	let currentCalls: CompactToolCall[] = [];
	let responseHistory: CompactToolHistory[] = [];
	let restoredHistoryBlocks = new Map<string, CompactToolHistory[]>();
	let hiddenRestoredHistoryEntries = new Set<string>();
	let turnHadText = false;
	let currentGoal = "Разбираюсь с задачей…";
	let lastWorkingStatus = "";

	const updateWorkingStatus = (ctx: ExtensionContext, status: string): void => {
		if (status === lastWorkingStatus) return;
		lastWorkingStatus = status;
		setWorkingStatus(ctx, status);
	};

	const flushHistory = (): void => {
		if (responseHistory.length === 0) return;
		pi.appendEntry<CompactToolHistoryBlock>(TOOL_HISTORY_BLOCK_ENTRY, {
			items: responseHistory.map((item) => ({ ...item })),
		});
		responseHistory = [];
	};

	const indexRestoredHistory = (ctx: ExtensionContext): void => {
		restoredHistoryBlocks = new Map();
		hiddenRestoredHistoryEntries = new Set();
		let ids: string[] = [];
		let items: CompactToolHistory[] = [];
		const flush = () => {
			if (ids.length > 1) {
				const visibleId = ids.at(-1);
				if (visibleId) restoredHistoryBlocks.set(visibleId, items.map((item) => ({ ...item })));
				for (const id of ids.slice(0, -1)) hiddenRestoredHistoryEntries.add(id);
			}
			ids = [];
			items = [];
		};
		for (const rawEntry of ctx.sessionManager.getEntries()) {
			const entry = asRecord(rawEntry);
			if (entry.type === "message" && asRecord(entry.message).role === "user") flush();
			if (entry.type !== "custom" || entry.customType !== TOOL_HISTORY_ENTRY) continue;
			const data = asRecord(entry.data);
			ids.push(String(entry.id));
			items.push({
				text: String(data.text ?? "Действие завершено"),
				isError: data.isError === true,
				durationMs: typeof data.durationMs === "number" ? data.durationMs : undefined,
			});
		}
		flush();
	};

	// Expand old grouped entries into one visible history line per tool.
	pi.registerEntryRenderer<CompactToolGroup>(TOOL_GROUP_ENTRY, (entry, _options, theme) => {
		const calls = entry.data?.calls ?? [];
		const lines = calls.map((call) => {
			const marker = call.isError ? "✗" : "✓";
			const text = call.args === undefined
				? legacyCompletedDescription(call)
				: completedToolDescription(call.name, call.args, call.isError);
			return theme.fg(call.isError ? "error" : "muted", `${marker} ${text}`);
		});
		return new Text(lines.join("\n"), 0, 0);
	});

	pi.registerEntryRenderer<CompactToolHistory>(TOOL_HISTORY_ENTRY, (entry, _options, theme) => {
		if (hiddenRestoredHistoryEntries.has(entry.id)) return undefined;
		const items = restoredHistoryBlocks.get(entry.id) ?? [entry.data ?? { text: "Действие завершено", isError: false }];
		const lines = items.map((item) => {
			const marker = item.isError ? "✗" : "✓";
			const duration = formatDuration(item.durationMs);
			const suffix = duration ? ` · ${duration}` : "";
			return theme.fg(item.isError ? "error" : "muted", `${marker} ${item.text}${suffix}`);
		});
		return new Text(lines.join("\n"), 0, 0);
	});

	pi.registerEntryRenderer<CompactToolHistoryBlock>(TOOL_HISTORY_BLOCK_ENTRY, (entry, _options, theme) => {
		const items = entry.data?.items ?? [];
		const lines = items.map((item) => {
			const marker = item.isError ? "✗" : "✓";
			const duration = formatDuration(item.durationMs);
			const suffix = duration ? ` · ${duration}` : "";
			return theme.fg(item.isError ? "error" : "muted", `${marker} ${item.text}${suffix}`);
		});
		return lines.length > 0 ? new Text(lines.join("\n"), 0, 0) : undefined;
	});

	pi.on("session_start", (_event, ctx) => {
		if (ctx.mode !== "tui") return;
		indexRestoredHistory(ctx);
		lastWorkingStatus = "";
		ctx.ui.setHiddenThinkingLabel("Ход работы скрыт · Ctrl+T");
		ctx.ui.setWorkingVisible(true);
	});

	pi.on("before_agent_start", (event, ctx) => {
		if (ctx.mode !== "tui") return;
		responseHistory = [];
		currentGoal = statusFromPrompt(event.prompt);
		ctx.ui.setWorkingVisible(true);
		updateWorkingStatus(ctx, currentGoal);
	});

	pi.on("turn_start", () => {
		currentCalls = [];
		turnHadText = false;
	});

	pi.on("message_update", (event, ctx) => {
		if (ctx.mode !== "tui") return;
		const update = event.assistantMessageEvent;
		if (update.type === "thinking_start") {
			ctx.ui.setWorkingVisible(false);
		} else if (update.type === "text_start") {
			turnHadText = true;
			flushHistory();
			ctx.ui.setWorkingVisible(false);
		}
	});

	pi.on("tool_execution_start", (event, ctx) => {
		if (ctx.mode !== "tui") return;
		ctx.ui.setWorkingVisible(false);
		const call: CompactToolCall = {
			id: event.toolCallId,
			name: event.toolName,
			args: event.args,
			description: describeToolCall(event.toolName, event.args),
			activity: statusForTool(event.toolName, event.args),
			startedAt: Date.now(),
			done: false,
			isError: false,
		};
		currentCalls.push(call);
		updateWorkingStatus(ctx, call.activity);
	});

	pi.on("tool_execution_end", (event, ctx) => {
		if (ctx.mode !== "tui") return;
		const call = currentCalls.find((item) => item.id === event.toolCallId);
		if (call && !call.done) {
			call.done = true;
			call.isError = event.isError;
			responseHistory.push({
				text: completedToolDescription(call.name, call.args, call.isError, event.result),
				isError: call.isError,
				durationMs: Math.max(0, Date.now() - call.startedAt),
			});
			const pending = [...currentCalls].reverse().find((item) => !item.done);
			const nextStatus = pending?.activity ?? statusAfterTool(call.name, call.args, call.isError, event.result);
			updateWorkingStatus(ctx, nextStatus);
			ctx.ui.setWorkingVisible(pending === undefined);
		}
	});

	pi.on("turn_end", (event) => {
		if (event.toolResults.length === 0 && !turnHadText) flushHistory();
		currentCalls = [];
	});

	pi.on("agent_end", (_event, ctx) => {
		if (ctx.mode !== "tui") return;
		flushHistory();
		lastWorkingStatus = "";
		ctx.ui.setWorkingMessage();
		ctx.ui.setWorkingVisible(true);
	});

	pi.on("session_shutdown", (_event, ctx) => {
		if (ctx.mode !== "tui") return;
		lastWorkingStatus = "";
		ctx.ui.setWorkingMessage();
		ctx.ui.setHiddenThinkingLabel();
		ctx.ui.setWorkingVisible(true);
	});

	// =========================================================================
	// Read Tool
	// =========================================================================
	pi.registerTool({
		name: "read",
		renderShell: "self",
		label: "read",
		description:
			"Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files.",
		parameters: getBuiltInTools(process.cwd()).read.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.read.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("read", args, theme, context);
			const path = shortenPath(args.path || "");
			let pathDisplay = path ? theme.fg("accent", path) : theme.fg("toolOutput", "...");

			// Show line range if specified
			if (args.offset !== undefined || args.limit !== undefined) {
				const startLine = args.offset ?? 1;
				const endLine = args.limit !== undefined ? startLine + args.limit - 1 : "";
				pathDisplay += theme.fg("warning", `:${startLine}${endLine ? `-${endLine}` : ""}`);
			}

			return new Text(`${theme.fg("toolTitle", theme.bold("read"))} ${pathDisplay}`, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			// Minimal mode: show nothing in collapsed state
			if (!expanded) {
				return new Text("", 0, 0);
			}

			// Expanded mode: show full output
			const textContent = result.content.find((c) => c.type === "text");
			if (!textContent || textContent.type !== "text") {
				return new Text("", 0, 0);
			}

			const lines = textContent.text.split("\n");
			const output = lines.map((line) => theme.fg("toolOutput", line)).join("\n");
			return new Text(`\n${output}`, 0, 0);
		},
	});

	// =========================================================================
	// Bash Tool
	// =========================================================================
	pi.registerTool({
		name: "bash",
		renderShell: "self",
		label: "bash",
		description:
			"Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last 2000 lines or 50KB (whichever is hit first).",
		parameters: getBuiltInTools(process.cwd()).bash.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.bash.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("bash", args, theme, context);
			const command = args.command || "...";
			const timeout = args.timeout as number | undefined;
			const timeoutSuffix = timeout ? theme.fg("muted", ` (timeout ${timeout}s)`) : "";

			return new Text(theme.fg("toolTitle", theme.bold(`$ ${command}`)) + timeoutSuffix, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			// Minimal mode: show nothing in collapsed state
			if (!expanded) {
				return new Text("", 0, 0);
			}

			// Expanded mode: show full output
			const textContent = result.content.find((c) => c.type === "text");
			if (!textContent || textContent.type !== "text") {
				return new Text("", 0, 0);
			}

			const output = textContent.text
				.trim()
				.split("\n")
				.map((line) => theme.fg("toolOutput", line))
				.join("\n");

			if (!output) {
				return new Text("", 0, 0);
			}

			return new Text(`\n${output}`, 0, 0);
		},
	});

	// =========================================================================
	// Write Tool
	// =========================================================================
	pi.registerTool({
		name: "write",
		renderShell: "self",
		label: "write",
		description:
			"Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		parameters: getBuiltInTools(process.cwd()).write.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.write.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("write", args, theme, context);
			const path = shortenPath(args.path || "");
			const pathDisplay = path ? theme.fg("accent", path) : theme.fg("toolOutput", "...");
			const lineCount = args.content ? args.content.split("\n").length : 0;
			const lineInfo = lineCount > 0 ? theme.fg("muted", ` (${lineCount} lines)`) : "";

			return new Text(`${theme.fg("toolTitle", theme.bold("write"))} ${pathDisplay}${lineInfo}`, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			// Minimal mode: show nothing (file was written)
			if (!expanded) {
				return new Text("", 0, 0);
			}

			// Expanded mode: show error if any
			if (result.content.some((c) => c.type === "text" && c.text)) {
				const textContent = result.content.find((c) => c.type === "text");
				if (textContent?.type === "text" && textContent.text) {
					return new Text(`\n${theme.fg("error", textContent.text)}`, 0, 0);
				}
			}

			return new Text("", 0, 0);
		},
	});

	// =========================================================================
	// Edit Tool
	// =========================================================================
	pi.registerTool({
		name: "edit",
		renderShell: "self",
		label: "edit",
		description:
			"Edit a file by replacing exact text. The oldText must match exactly (including whitespace). Use this for precise, surgical edits.",
		parameters: getBuiltInTools(process.cwd()).edit.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.edit.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("edit", args, theme, context);
			const path = shortenPath(args.path || "");
			const pathDisplay = path ? theme.fg("accent", path) : theme.fg("toolOutput", "...");

			return new Text(`${theme.fg("toolTitle", theme.bold("edit"))} ${pathDisplay}`, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			// Minimal mode: show nothing in collapsed state
			if (!expanded) {
				return new Text("", 0, 0);
			}

			// Expanded mode: show diff or error
			const textContent = result.content.find((c) => c.type === "text");
			if (!textContent || textContent.type !== "text") {
				return new Text("", 0, 0);
			}

			// For errors, show the error message
			const text = textContent.text;
			if (text.includes("Error") || text.includes("error")) {
				return new Text(`\n${theme.fg("error", text)}`, 0, 0);
			}

			// Otherwise show the text (would be nice to show actual diff here)
			return new Text(`\n${theme.fg("toolOutput", text)}`, 0, 0);
		},
	});

	// =========================================================================
	// Find Tool
	// =========================================================================
	pi.registerTool({
		name: "find",
		renderShell: "self",
		label: "find",
		description:
			"Find files by name pattern (glob). Searches recursively from the specified path. Output limited to 200 results.",
		parameters: getBuiltInTools(process.cwd()).find.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.find.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("find", args, theme, context);
			const pattern = args.pattern || "";
			const path = shortenPath(args.path || ".");
			const limit = args.limit;

			let text = `${theme.fg("toolTitle", theme.bold("find"))} ${theme.fg("accent", pattern)}`;
			text += theme.fg("toolOutput", ` in ${path}`);
			if (limit !== undefined) {
				text += theme.fg("toolOutput", ` (limit ${limit})`);
			}

			return new Text(text, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			if (!expanded) return new Text("", 0, 0);

			// Expanded: show full results
			const textContent = result.content.find((c) => c.type === "text");
			if (!textContent || textContent.type !== "text") {
				return new Text("", 0, 0);
			}

			const output = textContent.text
				.trim()
				.split("\n")
				.map((line) => theme.fg("toolOutput", line))
				.join("\n");

			return new Text(`\n${output}`, 0, 0);
		},
	});

	// =========================================================================
	// Grep Tool
	// =========================================================================
	pi.registerTool({
		name: "grep",
		renderShell: "self",
		label: "grep",
		description:
			"Search file contents by regex pattern. Uses ripgrep for fast searching. Output limited to 200 matches.",
		parameters: getBuiltInTools(process.cwd()).grep.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.grep.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("grep", args, theme, context);
			const pattern = args.pattern || "";
			const path = shortenPath(args.path || ".");
			const glob = args.glob;
			const limit = args.limit;

			let text = `${theme.fg("toolTitle", theme.bold("grep"))} ${theme.fg("accent", `/${pattern}/`)}`;
			text += theme.fg("toolOutput", ` in ${path}`);
			if (glob) {
				text += theme.fg("toolOutput", ` (${glob})`);
			}
			if (limit !== undefined) {
				text += theme.fg("toolOutput", ` limit ${limit}`);
			}

			return new Text(text, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			if (!expanded) return new Text("", 0, 0);

			// Expanded: show full results
			const textContent = result.content.find((c) => c.type === "text");
			if (!textContent || textContent.type !== "text") {
				return new Text("", 0, 0);
			}

			const output = textContent.text
				.trim()
				.split("\n")
				.map((line) => theme.fg("toolOutput", line))
				.join("\n");

			return new Text(`\n${output}`, 0, 0);
		},
	});

	// =========================================================================
	// Ls Tool
	// =========================================================================
	pi.registerTool({
		name: "ls",
		renderShell: "self",
		label: "ls",
		description:
			"List directory contents with file sizes. Shows files and directories with their sizes. Output limited to 500 entries.",
		parameters: getBuiltInTools(process.cwd()).ls.parameters,

		async execute(toolCallId, params, signal, onUpdate, ctx) {
			const tools = getBuiltInTools(ctx.cwd);
			return tools.ls.execute(toolCallId, params, signal, onUpdate);
		},

		renderCall(args, theme, context) {
			if (!context.expanded) return renderActiveToolCall("ls", args, theme, context);
			const path = shortenPath(args.path || ".");
			const limit = args.limit;

			let text = `${theme.fg("toolTitle", theme.bold("ls"))} ${theme.fg("accent", path)}`;
			if (limit !== undefined) {
				text += theme.fg("toolOutput", ` (limit ${limit})`);
			}

			return new Text(text, 0, 0);
		},

		renderResult(result, { expanded }, theme, _context) {
			if (!expanded) return new Text("", 0, 0);

			// Expanded: show full listing
			const textContent = result.content.find((c) => c.type === "text");
			if (!textContent || textContent.type !== "text") {
				return new Text("", 0, 0);
			}

			const output = textContent.text
				.trim()
				.split("\n")
				.map((line) => theme.fg("toolOutput", line))
				.join("\n");

			return new Text(`\n${output}`, 0, 0);
		},
	});
}
