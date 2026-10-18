// Python-compatible local timestamps and plain log lines.
import { inspect } from 'node:util';

// String(error) drops the stack and String({}) says "[object Object]";
// the stack trace is what a reader (or the cloud's AI assistant) needs.
export function messageText(message: unknown): string {
    if (typeof message === 'string') return message;
    if (message instanceof Error) return message.stack ?? String(message);
    return inspect(message, { breakLength: Infinity, depth: 4 });
}

export function timestamp(date = new Date()): string {
    const pad = (value: number, width = 2) => String(value).padStart(width, '0');
    return `${pad(date.getFullYear(), 4)}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
        `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
}

export function formatLine(prefix: string, message: string, withTime: boolean): string {
    return withTime ? `${prefix}:${timestamp()} - ${message}` : `${prefix}: ${message}`;
}
