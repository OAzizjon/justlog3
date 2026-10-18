// Buffered file logging and Python-compatible console levels and colours.
import { appendFileSync, writeFileSync } from 'node:fs';
import { formatLine, messageText } from './format.js';
import { enqueue } from './cloud.js';
import { loggers, positiveInteger, warn } from './state.js';

export interface LoggerOptions {
    withTime?: boolean;
    filemode?: 'a' | 'w';
    cycles?: number;
    cloudCycles?: number;
    file?: boolean;
}

export interface LogOptions {
    red?: boolean;
    green?: boolean;
    customPrefix?: string;
    offConsole?: boolean;
}

export class Logger {
    readonly filename: string;
    readonly withTime: boolean;
    readonly filemode: 'a' | 'w';
    readonly cycles: number;
    readonly cloudCycles: number;
    readonly file: boolean;
    /** @internal Cloud queue, retained independently of the disk buffer. */
    cloudLines: string[] = [];
    /** @internal Absolute position of the oldest queued line. */
    cloudHead = 0;
    /** @internal */
    overflowWarned = false;
    private buffer: string[] = [];

    constructor(filename = 'app.log', options: LoggerOptions = {}) {
        this.filename = filename.endsWith('.log') ? filename : `${filename}.log`;
        this.withTime = options.withTime ?? true;
        this.filemode = options.filemode ?? 'a';
        if (this.filemode !== 'a' && this.filemode !== 'w') {
            throw new TypeError('filemode must be a or w');
        }
        this.cycles = positiveInteger(options.cycles ?? 50, 'cycles');
        this.cloudCycles = positiveInteger(options.cloudCycles ?? 75, 'cloudCycles');
        this.file = options.file ?? true;
        if (this.file && this.filemode === 'w') writeFileSync(this.filename, '', 'utf8');
        loggers.add(this);
    }

    log(message: unknown = '[no text]', options: LogOptions = {}): void {
        const prefix = options.customPrefix || (options.red ? 'RED' : options.green ? 'GREEN' : 'GREY');
        const line = formatLine(prefix, messageText(message), this.withTime);
        if (!options.offConsole) {
            const tty = typeof process !== 'undefined' && process.stdout?.isTTY;
            const colour = options.red ? '\x1b[31m' : options.green ? '\x1b[92m' : '\x1b[90m';
            console.log(tty ? `${colour}${line}\x1b[0m` : line);
        }
        enqueue(this, line);
        if (this.file) {
            this.buffer.push(line);
            if (this.buffer.length >= this.cycles) this.flush();
        }
    }

    flush(): void {
        if (!this.buffer.length) return;
        const lines = this.buffer;
        this.buffer = [];
        try {
            appendFileSync(this.filename, lines.join('\n') + '\n', 'utf8');
        } catch (error) {
            // As in the Python client: a full or locked disk must not crash the
            // program from inside log(); show the lines that did not reach the file.
            warn(`Failed to write '${this.filename}': ${String(error)}`);
            console.error(`Unwritten logs:\n${lines.join('\n')}`);
        }
    }

    status() {
        return {
            loggerName: this.constructor.name,
            filename: this.filename,
            withTime: this.withTime,
            filemode: this.filemode,
            cycles: this.cycles,
            cloudCycles: this.cloudCycles,
            file: this.file,
        };
    }
}

const levels = { NOTSET: 0, DEBUG: 1, INFO: 2, WARNING: 3, ERROR: 4, SUCCESS: 4, CRITICAL: 5 };
export type ConsoleLevel = keyof typeof levels;
export interface BasicLoggerOptions extends LoggerOptions { consoleLevel?: string }

export class BasicLogger extends Logger {
    readonly consoleLevel: ConsoleLevel;

    constructor(filename = 'app.log', options: BasicLoggerOptions = {}) {
        super(filename, options);
        const level = (options.consoleLevel ?? 'DEBUG').toUpperCase();
        if (Object.hasOwn(levels, level)) {
            this.consoleLevel = level as ConsoleLevel;
        } else {
            this.consoleLevel = 'DEBUG';
            this.log(`Invalid value '${options.consoleLevel}' for consoleLevel. Valid options are: ` +
                'NOTSET, DEBUG, INFO, WARNING, ERROR / SUCCESS, CRITICAL',
            { red: true, customPrefix: '[JustLog3Error]' });
        }
    }

    private levelLog(level: ConsoleLevel, message: unknown): void {
        this.log(message, {
            customPrefix: level,
            red: level === 'WARNING' || level === 'ERROR' || level === 'CRITICAL',
            green: level === 'INFO' || level === 'SUCCESS',
            offConsole: levels[this.consoleLevel] > levels[level],
        });
    }

    debug(message: unknown = '[no text]'): void { this.levelLog('DEBUG', message); }
    info(message: unknown = '[no text]'): void { this.levelLog('INFO', message); }
    success(message: unknown = '[no text]'): void { this.levelLog('SUCCESS', message); }
    warning(message: unknown = '[no text]'): void { this.levelLog('WARNING', message); }
    error(message: unknown = '[no text]'): void { this.levelLog('ERROR', message); }
    critical(message: unknown = '[no text]'): void { this.levelLog('CRITICAL', message); }

    override status() { return { ...super.status(), consoleLevel: this.consoleLevel }; }
}
