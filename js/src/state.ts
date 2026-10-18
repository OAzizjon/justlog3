// Shared logger registry; cloud queues use absolute line positions for safe retries.
export interface CloudLogger {
    filename: string;
    withTime: boolean;
    cloudCycles: number;
    cloudLines: string[];
    cloudHead: number;
    overflowWarned: boolean;
    flush(): void;
}

export const loggers = new Set<CloudLogger>();

export function warn(message: string): void {
    console.error(`[JustLog3] ${message}`);
}

export function positiveInteger(value: number, name: string): number {
    if (!Number.isSafeInteger(value) || value < 1) {
        throw new TypeError(`${name} must be a positive integer`);
    }
    return value;
}
