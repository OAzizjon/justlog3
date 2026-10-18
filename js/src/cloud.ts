// Single cloud sender with bounded queues, stable retries and bounded shutdown.
import { randomBytes } from 'node:crypto';
// Node's timers have unref() in Node, Bun and Deno alike (Deno's global setTimeout returns a number).
import { clearTimeout, setTimeout } from 'node:timers';
import { loggers, positiveInteger, warn, type CloudLogger } from './state.js';

export interface CloudOptions {
    url?: string;
    flushIntervalMs?: number;
    timeoutMs?: number;
    maxBatchLines?: number;
}

interface Batch {
    head: number;
    lines: string[];
    seq: number;
}

const encoder = new TextEncoder();
let sender: CloudSender | undefined;

async function gzip(bytes: Uint8Array): Promise<Uint8Array> {
    try {
        const { gzipSync } = await import('node:zlib');
        return gzipSync(bytes);
    } catch {
        const stream = new Blob([bytes as BlobPart]).stream().pipeThrough(new CompressionStream('gzip'));
        return new Uint8Array(await new Response(stream).arrayBuffer());
    }
}

// Header values are byte strings and fetch throws on anything above U+00FF:
// send the UTF-8 bytes, as the Go client does, minus control characters.
function headerBytes(text: string): string {
    let out = '';
    for (const byte of encoder.encode(text)) {
        if (byte >= 32 && byte !== 127) out += String.fromCharCode(byte);
    }
    return out;
}

class CloudSender {
    readonly timeoutMs: number;
    // Per sender, as in the Python and Go clients: the server drops a repeated
    // session + Seq for 30 minutes, and every new sender restarts Seq at 1.
    private readonly session = randomBytes(16).toString('hex');
    private readonly interval: number;
    private readonly url: string;
    private batchLines: number;
    private seq = 0;
    private warned = false;
    private disabled = false;
    private stopped = false;
    private closing = false;
    private failed = false;
    // cloudCycles was reached while a request was on the wire: drain again after it.
    private wakeAgain = false;
    // Successful requests; an exit drain that adds none is not repeated.
    private acked = 0;
    private exitAckedAt = -1;
    private backoff: number;
    private retryAt = 0;
    private rateLimitUntil = 0;
    private timer?: ReturnType<typeof setTimeout>;
    private inFlight?: Promise<void>;
    private controller?: AbortController;
    private retries = new Map<CloudLogger, Batch>();
    private shutdownPromise?: Promise<void>;
    // Set on token replacement: each logger's queue end at that moment.
    private handover?: Map<CloudLogger, number>;

    /** `previous`: the replaced sender's shutdown, which must settle before this one sends. */
    constructor(private readonly token: string, options: Required<CloudOptions>, private readonly previous?: Promise<void>) {
        this.url = options.url;
        this.interval = options.flushIntervalMs;
        this.timeoutMs = options.timeoutMs;
        this.batchLines = options.maxBatchLines;
        this.backoff = this.interval;
        this.schedule();
    }

    accepts(): boolean { return !this.disabled && !this.stopped && !this.closing; }

    private warnOnce(message: string): void {
        if (!this.warned) { this.warned = true; warn(message); }
    }

    private schedule(): void {
        if (!this.accepts()) return;
        clearTimeout(this.timer);
        const delay = Math.max(this.interval, this.retryAt - Date.now(), this.rateLimitUntil - Date.now());
        this.timer = setTimeout(() => { void this.run(); }, delay);
        this.timer.unref?.();
    }

    wake(): void {
        if (!this.accepts() || this.failed || Date.now() < Math.max(this.retryAt, this.rateLimitUntil)) return;
        if (this.inFlight) { this.wakeAgain = true; return; }
        void this.run();
    }

    private run(deadline = Infinity): Promise<void> {
        if (this.inFlight) return this.inFlight;
        clearTimeout(this.timer);
        this.inFlight = Promise.resolve(this.previous).then(() => this.drain(deadline)).catch(error => {
            this.fail(`Cloud request failed: ${String(error)}; batch kept for retry.`);
        }).finally(() => {
            this.inFlight = undefined;
            this.schedule();
            if (this.wakeAgain) { this.wakeAgain = false; this.wake(); }
        });
        return this.inFlight;
    }

    private fail(message: string): void {
        this.failed = true;
        this.warnOnce(message);
        this.retryAt = Date.now() + this.backoff;
        this.backoff = Math.min(this.backoff * 2, 300_000);
    }

    private batch(logger: CloudLogger, max: number): Batch {
        const retry = this.retries.get(logger);
        if (retry && retry.head === logger.cloudHead) return retry;
        const lines: string[] = [];
        let bytes = 0;
        for (const line of logger.cloudLines) {
            const size = encoder.encode(line).length + (lines.length ? 1 : 0);
            if (lines.length && (lines.length >= Math.min(this.batchLines, max) || bytes + size > 512 * 1024)) break;
            lines.push(line);
            bytes += size;
        }
        const batch = { head: logger.cloudHead, lines, seq: ++this.seq };
        // Save before any async work: a timeout or failed compression must not change the batch.
        this.retries.set(logger, batch);
        return batch;
    }

    // After a token replacement this sender only sends what was logged before it;
    // read on every batch, so a drain already running obeys it too.
    private limitFor(logger: CloudLogger): number {
        return this.handover ? (this.handover.get(logger) ?? logger.cloudHead) : Infinity;
    }

    private async drain(deadline: number): Promise<void> {
        for (const logger of loggers) {
            const end = logger.cloudHead + logger.cloudLines.length;
            while (!this.stopped && !this.disabled && logger.cloudLines.length &&
                logger.cloudHead < Math.min(end, this.limitFor(logger))) {
                if (Date.now() >= deadline || Date.now() < Math.max(this.retryAt, this.rateLimitUntil)) return;
                const batch = this.batch(logger, this.limitFor(logger) - logger.cloudHead);
                if (!await this.send(logger, batch, deadline)) return;
            }
        }
    }

    private async send(logger: CloudLogger, batch: Batch, deadline: number): Promise<boolean> {
        const controller = new AbortController();
        this.controller = controller;
        const timeout = AbortSignal.timeout(Math.max(1, Math.min(this.timeoutMs, deadline - Date.now())));
        const abort = () => controller.abort();
        timeout.addEventListener('abort', abort, { once: true });
        let response: Response;
        try {
            let body = encoder.encode(batch.lines.join('\n'));
            const headers: Record<string, string> = {
                Authorization: `Bearer ${this.token}`,
                'Content-Type': 'text/plain; charset=utf-8',
                'X-JustLog-Session': this.session,
                'X-JustLog-Seq': String(batch.seq),
                'X-JustLog-Logger': headerBytes(logger.filename),
                'X-JustLog-UTC-Offset': logger.withTime ? String(-new Date().getTimezoneOffset() * 60) : '',
            };
            if (body.byteLength >= 1024) {
                body = new Uint8Array(await gzip(body));
                headers['Content-Encoding'] = 'gzip';
            }
            response = await fetch(this.url, {
                method: 'POST', headers, body: body as BodyInit, signal: controller.signal,
                redirect: 'manual',
            });
            // The protocol only needs response headers; cancel any unbounded response body.
            await response.body?.cancel();
        } catch (error) {
            if (!this.stopped) this.fail(`Cloud request failed (no answer): ${String(error)}; batch kept for retry.`);
            return false;
        } finally {
            timeout.removeEventListener('abort', abort);
            if (this.controller === controller) this.controller = undefined;
        }
        if (this.stopped) return false;
        const status = response.status;
        if (status === 401 || status === 403) {
            this.disabled = true;
            warn(`API token rejected (HTTP ${status}). Cloud sending stopped; files continue.`);
            return false;
        }
        if (status === 0 || status >= 300 && status < 400) {
            // Not followed: a 301/302 turns the POST into a GET and a cross-origin
            // hop drops Authorization, so every retry would fail the same way.
            this.disabled = true;
            const location = response.headers.get('Location');
            warn(`Cloud URL redirects (HTTP ${status}${location ? ` to ${location}` : ''}). ` +
                'Cloud sending stopped; files continue. Set url to the final address.');
            return false;
        }
        if (status >= 500) {
            this.fail(`Cloud server error (HTTP ${status}); batch kept for retry.`);
            return false;
        }
        this.retries.delete(logger);
        if (status === 429) {
            this.failed = true;
            const value = response.headers.get('Retry-After');
            const seconds = value?.trim() ? Number(value) : NaN;
            this.rateLimitUntil = Date.now() + (Number.isFinite(seconds) && seconds > 0 ? seconds : 60) * 1000;
            this.warnOnce('Cloud limit reached (HTTP 429); lines stay buffered until Retry-After.');
            return false;
        }
        if (status === 413 && batch.lines.length > 1) {
            this.batchLines = Math.max(1, Math.floor(batch.lines.length / 2));
            return true;
        }
        // Overflow may already have removed some or all of this batch while awaiting HTTP.
        const count = Math.max(0, batch.head + batch.lines.length - logger.cloudHead);
        logger.cloudLines.splice(0, count);
        logger.cloudHead += count;
        // The server answered, so the connection works: no backoff, wakes count again.
        this.failed = false;
        this.retryAt = 0;
        this.backoff = this.interval;
        if (status >= 400) {
            this.warnOnce(`Cloud rejected batch (HTTP ${status}); batch dropped.`);
        } else {
            this.acked++;
            this.warned = false;
            logger.overflowWarned = false;
            if (response.headers.has('X-JustLog-Lines-Rejected')) {
                this.warnOnce('Daily cloud limit reached; some lines were not stored.');
            }
        }
        return true;
    }

    stop(): void {
        this.stopped = true;
        clearTimeout(this.timer);
        this.controller?.abort();
    }

    /**
     * beforeExit: send what is waiting without closing, because a listener may
     * schedule more work (and log) after it. beforeExit then fires again, so an
     * exit drain that delivered nothing (server down, 429) is not repeated, or
     * the process would never exit.
     */
    exitDrain(): Promise<void> | undefined {
        if (!this.accepts() || this.acked === this.exitAckedAt) return undefined;
        let waiting = false;
        for (const logger of loggers) if (logger.cloudLines.length) waiting = true;
        if (!waiting) return undefined;
        this.exitAckedAt = this.acked;
        this.retryAt = 0; // One attempt during backoff; a 429 pause still holds.
        return this.run(Date.now() + this.timeoutMs);
    }

    /**
     * Token replacement: drain like shutdown(), but only the lines queued until
     * now. Settles after every earlier sender too: shutdown() alone gives up at
     * timeoutMs, and the next sender must not start while an older one still sends.
     */
    handOver(): Promise<void> {
        this.handover ??= new Map(Array.from(loggers, logger => [logger, logger.cloudHead + logger.cloudLines.length]));
        return Promise.all([this.shutdown(), this.previous]).then(() => undefined);
    }

    shutdown(): Promise<void> {
        if (this.shutdownPromise) return this.shutdownPromise;
        this.closing = true;
        clearTimeout(this.timer);
        const deadline = Date.now() + this.timeoutMs;
        this.shutdownPromise = (async () => {
            let limit: ReturnType<typeof setTimeout> | undefined;
            const work = async () => {
                await this.inFlight;
                this.retryAt = 0; // One final attempt during backoff; respect a 429 pause.
                if (Date.now() < deadline) await this.run(deadline);
            };
            try {
                await Promise.race([
                    work(),
                    new Promise<void>(resolve => { limit = setTimeout(resolve, this.timeoutMs); }),
                ]);
            } finally {
                clearTimeout(limit);
                this.stop();
            }
        })();
        return this.shutdownPromise;
    }
}

export function enqueue(logger: CloudLogger, line: string): void {
    if (!sender?.accepts()) return;
    logger.cloudLines.push(line);
    if (logger.cloudLines.length > 10_000) {
        const count = logger.cloudLines.length - 10_000;
        logger.cloudLines.splice(0, count);
        logger.cloudHead += count;
        if (!logger.overflowWarned) {
            logger.overflowWarned = true;
            warn(`Cloud buffer for '${logger.filename}' overflowed; oldest lines dropped.`);
        }
    }
    if (logger.cloudLines.length >= logger.cloudCycles) sender.wake();
}

export function setApiToken(token: string, options: CloudOptions = {}): void {
    if (typeof token !== 'string' || !token.trim()) throw new TypeError('token must be a non-empty string');
    const config = {
        url: options.url ?? 'https://jl3-cloud.site/api/logs/',
        flushIntervalMs: positiveInteger(options.flushIntervalMs ?? 5000, 'flushIntervalMs'),
        timeoutMs: positiveInteger(options.timeoutMs ?? 5000, 'timeoutMs'),
        maxBatchLines: positiveInteger(options.maxBatchLines ?? 5000, 'maxBatchLines'),
    };
    let url: URL;
    try { url = new URL(config.url); } catch { throw new TypeError('url must be an http(s) URL'); }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new TypeError('url must be an http(s) URL');
    // As in Python and Go, the old sender drains first: a batch it sent or must
    // retry keeps its session and Seq, so the server stores it once, not again
    // under the new session. Lines logged from now on wait for the new token.
    sender = new CloudSender(token.trim(), config, sender?.handOver());
}

/**
 * Flush files and send the remaining cloud backlog within timeoutMs.
 * Applications may opt in to signal handling:
 * process.on('SIGTERM', async () => { await shutdown(); process.exit(0) });
 */
export async function shutdown(): Promise<void> {
    for (const logger of loggers) logger.flush();
    await sender?.shutdown();
}

if (typeof process !== 'undefined' && typeof process.on === 'function') {
    process.on('exit', () => {
        for (const logger of loggers) logger.flush();
    });
    // Not shutdown(): that ends cloud sending, and lines logged by work another
    // beforeExit listener schedules would be dropped.
    process.on('beforeExit', () => {
        // A microtask runs after the other beforeExit listeners, which may log
        // synchronously, and unlike setImmediate it keeps the loop alive only
        // when a request actually starts.
        queueMicrotask(() => {
            for (const logger of loggers) logger.flush();
            void sender?.exitDrain();
        });
    });
}
