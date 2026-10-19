// Run each sample in a fresh process; keep setup and verification out of the timer.
import { fork } from 'node:child_process';
import { once } from 'node:events';
import {
    appendFileSync, closeSync, createWriteStream, mkdtempSync, openSync, readFileSync, rmSync,
} from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const N = 50_000;
const RUNS = 7;
const MESSAGE = 'user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms';
const VARIANTS = ['justlog3', 'pino async', 'pino sync', 'winston', 'fs.appendFileSync'];
const SCENARIOS = ['file', 'file + console'];
const script = fileURLToPath(import.meta.url);

async function worker(variant, scenario, filename) {
    const consoleOutput = scenario === 'file + console';
    let log;
    let finish;

    if (variant === 'justlog3') {
        const { Logger, shutdown } = await import('../../js/dist/index.js');
        const logger = new Logger(filename, { withTime: true, filemode: 'w' });
        const options = { offConsole: !consoleOutput };
        log = () => logger.log(MESSAGE, options);
        finish = async () => {
            logger.flush();
            await shutdown();
        };
    } else if (variant.startsWith('pino ')) {
        const { default: pino } = await import('pino');
        const sync = variant === 'pino sync';
        const destination = pino.destination({ dest: filename, sync });
        // Wait for the asynchronous destination to open before starting the timer.
        if (destination.fd < 0) await once(destination, 'ready');
        const stream = consoleOutput
            ? pino.multistream([{ stream: destination }, { stream: process.stdout }])
            : destination;
        const logger = pino({ timestamp: pino.stdTimeFunctions.isoTime }, stream);
        log = () => logger.info(MESSAGE);
        finish = async () => {
            if (sync) destination.flushSync();
            else await new Promise((resolve, reject) => {
                destination.flush(error => error ? reject(error) : resolve());
            });
            const closed = once(destination, 'close');
            destination.end();
            await closed;
        };
    } else if (variant === 'winston') {
        const { default: winston } = await import('winston');
        // Supply the file stream so we can await its actual descriptor close,
        // rather than only the transport's writable-side finish event.
        const output = createWriteStream(filename, { flags: 'w' });
        await once(output, 'open');
        const file = new winston.transports.File({ stream: output });
        const transports = [file];
        if (consoleOutput) transports.push(new winston.transports.Console());
        const logger = winston.createLogger({
            format: winston.format.combine(
                winston.format.timestamp(),
                winston.format.printf(info => `${info.timestamp} ${info.message}`),
            ),
            transports,
        });
        // Propagate transport failures to the worker.
        logger.on('error', error => { throw error; });
        log = () => logger.info(MESSAGE);
        finish = async () => {
            const finished = once(logger, 'finish');
            const closed = once(output, 'close');
            logger.end();
            await finished;
            await closed;
            logger.close();
        };
    } else if (variant === 'fs.appendFileSync') {
        log = () => {
            const line = `${new Date().toISOString()} ${MESSAGE}`;
            appendFileSync(filename, `${line}\n`, 'utf8');
            if (consoleOutput) console.log(line);
        };
        finish = async () => {}; // appendFileSync opens, writes and closes on every call.
    } else {
        throw new Error(`Unknown variant: ${variant}`);
    }

    const start = process.hrtime.bigint();
    for (let i = 0; i < N; i++) log();
    await finish();
    if (consoleOutput) {
        // Include any queued console writes before stopping the clock.
        await new Promise((resolve, reject) => {
            process.stdout.write('', error => error ? reject(error) : resolve());
        });
    }
    const microsecondsPerCall = Number(process.hrtime.bigint() - start) / 1_000 / N;

    const contents = readFileSync(filename, 'utf8');
    const lines = contents.split('\n');
    if (lines.pop() !== '' || lines.length !== N) {
        throw new Error(`${variant} (${scenario}): expected ${N} complete lines, got ${lines.length}`);
    }
    for (const [index, line] of lines.entries()) {
        if (!line.includes(MESSAGE) || !/\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}/.test(line)) {
            throw new Error(`${variant} (${scenario}): missing message or timestamp at line ${index + 1}`);
        }
    }
    process.send({ microsecondsPerCall });
    process.disconnect();
}

function sample(variant, scenario, filename) {
    return new Promise((resolve, reject) => {
        const nullFd = openSync(os.devNull, 'w');
        let child;
        try {
            child = fork(script, ['--worker', variant, scenario, filename], {
                stdio: ['ignore', nullFd, 'inherit', 'ipc'],
            });
        } finally {
            closeSync(nullFd);
        }
        let result;
        const timeout = setTimeout(() => {
            child.kill();
            reject(new Error(`${variant} (${scenario}): timed out after 120 seconds`));
        }, 120_000);
        child.on('message', message => { result = message.microsecondsPerCall; });
        child.on('error', error => {
            clearTimeout(timeout);
            reject(error);
        });
        child.on('exit', (code, signal) => {
            clearTimeout(timeout);
            if (code !== 0 || !Number.isFinite(result)) {
                reject(new Error(`${variant} (${scenario}): worker failed (${signal ?? code})`));
            } else resolve(result);
        });
    });
}

async function main() {
    const directory = mkdtempSync(path.join(os.tmpdir(), 'justlog3-bench-'));
    const cases = SCENARIOS.flatMap(scenario => VARIANTS.map(variant => ({
        variant, scenario, values: [],
    })));
    console.log(`# Node.js logging benchmark\n`);
    console.log(`Node: ${process.version}; OS: ${os.platform()} ${os.release()} ${os.arch()}`);
    console.log(`CPU: ${os.cpus()[0]?.model ?? 'unknown'}; RAM: ${os.totalmem()} bytes`);
    console.log(`Calls/run: ${N.toLocaleString('en-US')}; runs/case: ${RUNS}; fresh process per run`);
    console.log('Timing includes logging, final flush/close and console drain; excludes setup and verification.');
    console.log('Console stdout goes to the OS null device. No fsync or physical-disk durability guarantee.\n');
    try {
        for (let round = 0; round < RUNS; round++) {
            // Interleave libraries in every round; rotate the first case to reduce order bias.
            const order = [...cases.slice(round), ...cases.slice(0, round)];
            for (const entry of order) {
                const filename = path.join(directory, 'sample.log');
                entry.values.push(await sample(entry.variant, entry.scenario, filename));
                rmSync(filename);
            }
            console.error(`Completed round ${round + 1}/${RUNS}`);
        }
        console.log('| Scenario | Logger | Median (µs/call) | Min–max (µs/call) |');
        console.log('| --- | --- | ---: | ---: |');
        for (const { variant, scenario, values } of cases) {
            values.sort((a, b) => a - b);
            console.log(`| ${scenario} | ${variant} | ${values[3].toFixed(3)} | ` +
                `${values[0].toFixed(3)}–${values[6].toFixed(3)} |`);
        }
    } finally {
        rmSync(directory, { recursive: true, force: true });
    }
}

try {
    if (process.argv[2] === '--worker') await worker(...process.argv.slice(3));
    else await main();
} catch (error) {
    console.error(error);
    process.exitCode = 1;
    if (process.connected) process.disconnect();
}
