// Exercise the published bundles and actual filesystem/console behaviour.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, existsSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import { Logger, BasicLogger, shutdown } from '../dist/index.js';

test('Python line format, red precedence, custom prefixes and defaults', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'jl3-'));
    try {
        const logger = new Logger(join(dir, 'format'), { cycles: 1 });
        logger.log('hello', { red: true, green: true, offConsole: true });
        const line = readFileSync(logger.filename, 'utf8');
        const match = /^RED:(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})\.(\d{3}) - hello\n$/.exec(line);
        assert.ok(match);
        const local = new Date(...match.slice(1).map(Number).map((n, i) => i === 1 ? n - 1 : n));
        assert.ok(Math.abs(Date.now() - local.getTime()) < 2000);
        const plain = new Logger(join(dir, 'plain.log'), { withTime: false, cycles: 1 });
        plain.log(undefined, { offConsole: true });
        plain.log('ok', { green: true, customPrefix: 'CUSTOM', offConsole: true });
        assert.equal(readFileSync(plain.filename, 'utf8'), 'GREY: [no text]\nCUSTOM: ok\n');
        await shutdown();
    } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('file batches, explicit flush, and truncate once in w mode', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'jl3-'));
    try {
        const filename = join(dir, 'batch.log');
        writeFileSync(filename, 'old\n');
        const logger = new Logger(filename, { withTime: false, filemode: 'w', cycles: 2 });
        assert.equal(readFileSync(filename, 'utf8'), '');
        logger.log('one', { offConsole: true });
        assert.equal(readFileSync(filename, 'utf8'), '');
        logger.log('two', { offConsole: true });
        assert.equal(readFileSync(filename, 'utf8'), 'GREY: one\nGREY: two\n');
        logger.log('three', { offConsole: true });
        logger.flush();
        assert.equal(readFileSync(filename, 'utf8'), 'GREY: one\nGREY: two\nGREY: three\n');
        const noFile = new Logger(join(dir, 'absent'), { file: false });
        noFile.log('serverless', { offConsole: true });
        await shutdown();
        assert.equal(existsSync(noFile.filename), false);
    } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('BasicLogger console threshold leaves all levels in the file', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'jl3-'));
    const original = console.log;
    const output = [];
    console.log = line => output.push(line);
    try {
        const logger = new BasicLogger(join(dir, 'levels'), { withTime: false, consoleLevel: 'ERROR' });
        for (const level of ['debug', 'info', 'success', 'warning', 'error', 'critical']) logger[level](level);
        logger.flush();
        assert.equal(readFileSync(logger.filename, 'utf8'),
            'DEBUG: debug\nINFO: info\nSUCCESS: success\nWARNING: warning\nERROR: error\nCRITICAL: critical\n');
        assert.equal(output.length, 3);
        assert.equal(logger.status().consoleLevel, 'ERROR');
        await shutdown();
    } finally {
        console.log = original;
        rmSync(dir, { recursive: true, force: true });
    }
});

test('errors keep their stack trace and objects stay readable', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'jl3-'));
    try {
        const logger = new Logger(join(dir, 'values'), { withTime: false, cycles: 1 });
        logger.log(new Error('payment failed'), { red: true, offConsole: true });
        logger.log({ user: 7, tags: ['a'] }, { offConsole: true });
        logger.log(42, { offConsole: true });
        const text = readFileSync(logger.filename, 'utf8');
        assert.match(text, /^RED: Error: payment failed\n {4}at /);
        assert.ok(text.endsWith("GREY: { user: 7, tags: [ 'a' ] }\nGREY: 42\n"));
        await shutdown();
    } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('a failed disk write warns with the lines instead of throwing', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'jl3-'));
    const original = console.error;
    const errors = [];
    console.error = line => errors.push(line);
    try {
        // A directory where the file should be makes appendFileSync fail on every platform.
        const logger = new Logger(join(dir, 'blocked.log'), { withTime: false, cycles: 2 });
        mkdirSync(logger.filename);
        logger.log('one', { offConsole: true });
        assert.doesNotThrow(() => logger.log('two', { offConsole: true }));
        assert.match(errors[0], /^\[JustLog3\] Failed to write '.*blocked\.log': /);
        assert.equal(errors[1], 'Unwritten logs:\nGREY: one\nGREY: two');
        logger.log('three', { offConsole: true });
        await shutdown();
        assert.equal(errors.length, 4);
    } finally {
        console.error = original;
        rmSync(dir, { recursive: true, force: true });
    }
});

test('CommonJS exports work', () => {
    const require = createRequire(import.meta.url);
    assert.equal(typeof require('../dist/index.cjs').Logger, 'function');
});

test('console output has no ANSI when piped', () => {
    const script = `import { Logger } from './dist/index.js';
        const logger = new Logger('unused', { file: false, withTime: false });
        logger.log('red', { red: true }); logger.log('green', { green: true }); logger.log('grey');`;
    const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], { encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, 'RED: red\nGREEN: green\nGREY: grey\n');
});

test('exit writes file buffers synchronously without signal listeners', () => {
    const dir = mkdtempSync(join(tmpdir(), 'jl3-'));
    try {
        const filename = join(dir, 'exit.log');
        const script = `import { Logger } from './dist/index.js';
            const logger = new Logger(${JSON.stringify(filename)}, { withTime: false });
            logger.log('exit', { offConsole: true });
            if (process.listenerCount('SIGTERM') || process.listenerCount('SIGINT')) process.exit(2);
            process.exit(0);`;
        const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], { encoding: 'utf8' });
        assert.equal(result.status, 0, result.stderr);
        assert.equal(readFileSync(filename, 'utf8'), 'GREY: exit\n');
    } finally { rmSync(dir, { recursive: true, force: true }); }
});
