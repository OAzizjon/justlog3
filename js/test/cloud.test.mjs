// Local HTTP contract tests; no calls to the public cloud service.
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { gunzipSync } from 'node:zlib';
import { setTimeout as delay } from 'node:timers/promises';
import { spawn } from 'node:child_process';
import { Logger, setApiToken, shutdown } from '../dist/index.js';

async function until(predicate, timeout = 2500) {
    const end = Date.now() + timeout;
    while (!predicate()) {
        if (Date.now() > end) assert.fail('timed out waiting for cloud request');
        await delay(5);
    }
}

async function fixture(run, options = {}) {
    const requests = [];
    let handler = (_request, response) => response.end();
    const server = createServer(async (request, response) => {
        const chunks = [];
        for await (const chunk of request) chunks.push(chunk);
        const raw = Buffer.concat(chunks);
        const body = request.headers['content-encoding'] === 'gzip' ? gunzipSync(raw).toString() : raw.toString();
        const captured = { headers: request.headers, body, time: Date.now() };
        requests.push(captured);
        handler(captured, response, requests.length);
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    const url = `http://127.0.0.1:${server.address().port}/api/logs/`;
    setApiToken(' token ', { url, flushIntervalMs: 20, timeoutMs: 100, ...options });
    const logger = new Logger('cloud-contract', { file: false, withTime: false, cloudCycles: 1 });
    try {
        await run({ logger, requests, url, handle: fn => { handler = fn; } });
    } finally {
        await shutdown();
        // Clear any queues retained by a disabled sender before the next fixture.
        handler = (_request, response) => response.end();
        setApiToken('cleanup', { url, flushIntervalMs: 20, timeoutMs: 1000 });
        await shutdown();
        server.closeAllConnections();
        await new Promise(resolve => server.close(resolve));
    }
}

test('validates token, URL and numeric options', () => {
    for (const token of ['', '  ', null]) assert.throws(() => setApiToken(token), TypeError);
    for (const url of ['file:///tmp/log', 'no-url', 'ftp://example.com']) {
        assert.throws(() => setApiToken('token', { url }), TypeError);
    }
    assert.throws(() => setApiToken('token', { maxBatchLines: 0 }), TypeError);
});

test('headers, UTF-8 body, gzip at 1 KB, and sender session', async () => {
    await fixture(async ({ logger, requests }) => {
        logger.log('small', { offConsole: true });
        await until(() => requests.length === 1);
        logger.log('é'.repeat(600), { offConsole: true });
        await until(() => requests.length === 2);
        const [first, second] = requests;
        assert.equal(first.headers.authorization, 'Bearer token');
        assert.equal(first.headers['content-type'], 'text/plain; charset=utf-8');
        assert.match(first.headers['x-justlog-session'], /^[0-9a-f]{32}$/);
        assert.equal(first.headers['x-justlog-logger'], 'cloud-contract.log');
        assert.equal(first.headers['x-justlog-utc-offset'], '');
        assert.equal(first.headers['x-justlog-seq'], '1');
        assert.equal(first.headers['content-encoding'], undefined);
        assert.equal(first.body, 'GREY: small');
        assert.equal(second.headers['content-encoding'], 'gzip');
        assert.equal(second.body, 'GREY: ' + 'é'.repeat(600));
        assert.equal(second.headers['x-justlog-session'], first.headers['x-justlog-session']);
        const timed = new Logger('timed', { file: false, cloudCycles: 1 });
        timed.log('time', { offConsole: true });
        await until(() => requests.length === 3);
        assert.equal(requests[2].headers['x-justlog-utc-offset'], String(-new Date().getTimezoneOffset() * 60));
    });
});

test('token replacement starts a new session and restarts sequence at 1', async () => {
    await fixture(async ({ logger, requests, url }) => {
        logger.log('before replacement', { offConsole: true });
        await shutdown();
        setApiToken('token', { url, flushIntervalMs: 20, timeoutMs: 500 });
        logger.log('after replacement', { offConsole: true });
        await until(() => requests.length === 2, 1500);
        await shutdown();
        const [first, second] = requests;
        assert.equal(first.body, 'GREY: before replacement');
        assert.equal(second.body, 'GREY: after replacement');
        assert.match(first.headers['x-justlog-session'], /^[0-9a-f]{32}$/);
        assert.match(second.headers['x-justlog-session'], /^[0-9a-f]{32}$/);
        assert.notEqual(second.headers['x-justlog-session'], first.headers['x-justlog-session']);
        assert.deepEqual(requests.map(request => request.headers['x-justlog-seq']), ['1', '1']);
    });
});

test('non-ASCII logger names are sent as UTF-8 header bytes', async () => {
    await fixture(async ({ requests }) => {
        const logger = new Logger('журнал', { file: false, withTime: false, cloudCycles: 1 });
        logger.log('delivered', { offConsole: true });
        await until(() => requests.length === 1, 1500);
        await shutdown();
        const filename = Buffer.from(requests[0].headers['x-justlog-logger'], 'latin1').toString('utf8');
        assert.equal(filename, 'журнал.log');
        assert.equal(requests[0].body, 'GREY: delivered');
    });
});

test('gzip starts at exactly 1024 body bytes', async () => {
    await fixture(async ({ logger, requests }) => {
        for (const [index, size] of [1023, 1024].entries()) {
            logger.log('x'.repeat(size - Buffer.byteLength('GREY: ')), { offConsole: true });
            await until(() => requests.length === index + 1, 1500);
        }
        await shutdown();
        assert.deepEqual(requests.map(request => Buffer.byteLength(request.body)), [1023, 1024]);
        assert.deepEqual(requests.map(request => request.headers['content-encoding']), [undefined, 'gzip']);
        assert.deepEqual(requests.map(request => request.body), [
            'GREY: ' + 'x'.repeat(1017),
            'GREY: ' + 'x'.repeat(1018),
        ]);
    });
});

for (const failure of ['503', 'timeout']) {
    test(`retains exact batch and sequence after ${failure}`, async () => {
        await fixture(async ({ logger, requests, handle }) => {
            handle((_request, response, count) => {
                if (count === 1) {
                    if (failure === '503') { response.writeHead(503); response.end(); }
                    return;
                }
                response.end();
            });
            logger.log('first', { offConsole: true });
            await until(() => requests.length === 1);
            logger.log('later', { offConsole: true });
            await until(() => requests.length >= 3);
            assert.equal(requests[1].body, requests[0].body);
            assert.equal(requests[1].headers['x-justlog-seq'], requests[0].headers['x-justlog-seq']);
            assert.equal(requests[2].body, 'GREY: later');
            assert.notEqual(requests[2].headers['x-justlog-seq'], requests[0].headers['x-justlog-seq']);
        }, { flushIntervalMs: 40, timeoutMs: 60 });
    });
}

test('429 respects Retry-After even when more lines wake the sender', async () => {
    await fixture(async ({ logger, requests, handle }) => {
        handle((_request, response, count) => {
            if (count === 1) response.writeHead(429, { 'Retry-After': '1' });
            response.end();
        });
        logger.log('wait', { offConsole: true });
        await until(() => requests.length === 1);
        await delay(20);
        logger.log('also wait', { offConsole: true });
        await delay(80);
        assert.equal(requests.length, 1);
        await until(() => requests.length >= 2);
        assert.ok(requests[1].time - requests[0].time >= 990);
        assert.equal(requests[1].body, 'GREY: wait\nGREY: also wait');
    });
});

test('401 stops sending until token replacement', async () => {
    await fixture(async ({ logger, requests, handle }) => {
        handle((_request, response) => { response.writeHead(401); response.end(); });
        logger.log('rejected', { offConsole: true });
        await until(() => requests.length === 1);
        await delay(30);
        logger.log('disabled', { offConsole: true });
        await delay(80);
        assert.equal(requests.length, 1);
    });
});

test('413 halves line count and immediately retries without losing lines', async () => {
    await fixture(async ({ logger, requests, handle }) => {
        handle((_request, response, count) => {
            if (count === 1) response.writeHead(413);
            response.end();
        });
        for (let i = 0; i < 4; i++) logger.log(String(i), { offConsole: true });
        await until(() => requests.length === 3);
        assert.deepEqual(requests.map(r => r.body.split('\n').length), [4, 2, 2]);
        assert.equal(requests.slice(1).map(r => r.body).join('\n'), requests[0].body);
    });
});

test('a single line rejected with 413 is dropped once and the next line is delivered', async () => {
    const original = console.error;
    const warnings = [];
    console.error = message => warnings.push(message);
    try {
        await fixture(async ({ logger, requests, handle }) => {
            handle((_request, response, count) => {
                if (count === 1) response.writeHead(413);
                response.end();
            });
            logger.log('rejected', { offConsole: true });
            await until(() => warnings.length === 1, 1500);
            logger.log('next', { offConsole: true });
            await until(() => requests.length === 2, 1500);
            await shutdown();
            assert.deepEqual(requests.map(request => request.body), ['GREY: rejected', 'GREY: next']);
            assert.deepEqual(warnings, ['[JustLog3] Cloud rejected batch (HTTP 413); batch dropped.']);
        });
    } finally { console.error = original; }
});

test('lines queued during a successful request arrive exactly once', async () => {
    await fixture(async ({ logger, requests, handle }) => {
        let firstResponse;
        handle((_request, response, count) => {
            if (count === 1) firstResponse = response;
            else response.end();
        });
        try {
            logger.log('first', { offConsole: true });
            await until(() => requests.length === 1, 1500);
            logger.log('second', { offConsole: true });
            logger.log('third', { offConsole: true });
            await delay(40);
            assert.equal(requests.length, 1);
            firstResponse.end();
            await until(() => requests.flatMap(request => request.body.split('\n')).length >= 3, 1500);
            await shutdown();
            assert.deepEqual(requests.flatMap(request => request.body.split('\n')), [
                'GREY: first', 'GREY: second', 'GREY: third',
            ]);
        } finally { firstResponse?.end(); }
    }, { timeoutMs: 500 });
});

test('a successful response reporting rejected lines warns once', async () => {
    const original = console.error;
    const warnings = [];
    console.error = message => warnings.push(message);
    try {
        await fixture(async ({ logger, requests, handle }) => {
            handle((_request, response) => {
                response.writeHead(204, { 'X-JustLog-Lines-Rejected': '3' });
                response.end();
            });
            for (let i = 0; i < 3; i++) logger.log(String(i), { offConsole: true });
            await until(() => warnings.length === 1, 1500);
            await shutdown();
            assert.deepEqual(requests.map(request => request.body), ['GREY: 0\nGREY: 1\nGREY: 2']);
            assert.deepEqual(warnings, ['[JustLog3] Daily cloud limit reached; some lines were not stored.']);
        });
    } finally { console.error = original; }
});

test('oldest lines dropped at 10,000 with one overflow warning', async () => {
    const original = console.error;
    const warnings = [];
    console.error = message => warnings.push(message);
    try {
        await fixture(async ({ requests }) => {
            const logger = new Logger('overflow', { file: false, withTime: false, cloudCycles: 20_000 });
            for (let i = 0; i < 10_005; i++) logger.log(String(i), { offConsole: true });
            await until(() => requests.length === 2);
            const lines = requests.flatMap(request => request.body.split('\n'));
            assert.equal(lines.length, 10_000);
            assert.equal(lines[0], 'GREY: 5');
            assert.equal(lines.at(-1), 'GREY: 10004');
            assert.equal(warnings.filter(message => message.includes('overflowed')).length, 1);
        });
    } finally { console.error = original; }
});

test('512 KB cap, maxBatchLines and one oversized line always sent', async () => {
    await fixture(async ({ logger, requests }) => {
        for (const size of [300_000, 300_000, 600_000]) logger.log('x'.repeat(size), { offConsole: true });
        await until(() => requests.length === 3);
        assert.deepEqual(requests.map(request => request.body.length), [300_006, 300_006, 600_006]);
    }, { maxBatchLines: 2 });
});

test('512 KiB cap counts UTF-8 bytes and joining newlines without losing lines', async () => {
    await fixture(async ({ logger, requests }) => {
        // Two lines fill the cap before the joining LF, so they must be separate batches.
        const messages = Array.from({ length: 6 }, (_, i) => `${i}:` + 'ж'.repeat(131_068));
        const expected = messages.map(message => 'GREY: ' + message);
        assert.equal(Buffer.byteLength(expected[0]), 262_144);
        for (const message of messages) logger.log(message, { offConsole: true });
        await until(() => requests.flatMap(request => request.body.split('\n')).length >= messages.length, 1500);
        await shutdown();
        for (const request of requests) assert.ok(Buffer.byteLength(request.body) <= 524_288);
        assert.deepEqual(requests.flatMap(request => request.body.split('\n')), expected);
    }, { timeoutMs: 1000 });
});

test('shutdown sends the remaining backlog and bounds a hanging request', async () => {
    await fixture(async ({ logger, requests, handle }) => {
        logger.log('final', { offConsole: true });
        await shutdown();
        assert.equal(requests[0].body, 'GREY: final');
        handle(() => {});
    });
    await fixture(async ({ logger, handle }) => {
        handle(() => {});
        logger.log('hang', { offConsole: true });
        const start = Date.now();
        await shutdown();
        assert.ok(Date.now() - start < 400);
    }, { timeoutMs: 60 });
});

test('beforeExit sends backlog while the unref timer permits natural exit', async () => {
    await fixture(async ({ requests, url }) => {
        const script = `import { Logger, setApiToken } from './dist/index.js';
            setApiToken('child', { url: ${JSON.stringify(url)}, flushIntervalMs: 60000, timeoutMs: 1000 });
            new Logger('child', { file: false, withTime: false }).log('natural exit', { offConsole: true });`;
        const child = spawn(process.execPath, ['--input-type=module', '-e', script], { stdio: ['ignore', 'ignore', 'pipe'] });
        let stderr = '';
        child.stderr.on('data', data => { stderr += data; });
        const guard = setTimeout(() => child.kill(), 3000);
        try {
            const code = await new Promise((resolve, reject) => {
                child.once('error', reject);
                child.once('exit', resolve);
            });
            assert.equal(code, 0, stderr);
            assert.equal(requests.length, 1);
            assert.equal(requests[0].body, 'GREY: natural exit');
        } finally { clearTimeout(guard); }
    });
});

async function runChild(script, timeout = 4000) {
    const child = spawn(process.execPath, ['--input-type=module', '-e', script], { stdio: ['ignore', 'ignore', 'pipe'] });
    let stderr = '';
    child.stderr.on('data', data => { stderr += data; });
    const start = Date.now();
    const guard = setTimeout(() => child.kill(), timeout);
    try {
        const code = await new Promise((resolve, reject) => {
            child.once('error', reject);
            child.once('exit', resolve);
        });
        return { code, stderr, ms: Date.now() - start };
    } finally { clearTimeout(guard); }
}

test('lines logged by another beforeExit listener still reach the cloud', async () => {
    await fixture(async ({ requests, url }) => {
        const { code, stderr } = await runChild(`import { Logger, setApiToken } from './dist/index.js';
            setApiToken('child', { url: ${JSON.stringify(url)}, flushIntervalMs: 60000, timeoutMs: 1000 });
            const logger = new Logger('child', { file: false, withTime: false });
            logger.log('first', { offConsole: true });
            process.once('beforeExit', () => setTimeout(() => logger.log('closed', { offConsole: true }), 20));`);
        assert.equal(code, 0, stderr);
        assert.deepEqual(requests.flatMap(request => request.body.split('\n')), ['GREY: first', 'GREY: closed']);
    });
});

test('an exit drain that fails is not repeated, so the process still exits', async () => {
    await fixture(async ({ requests, url, handle }) => {
        handle((_request, response) => { response.writeHead(503); response.end(); });
        const { code, stderr, ms } = await runChild(`import { Logger, setApiToken } from './dist/index.js';
            setApiToken('child', { url: ${JSON.stringify(url)}, flushIntervalMs: 60000, timeoutMs: 1000 });
            new Logger('child', { file: false, withTime: false }).log('lost', { offConsole: true });`);
        assert.equal(code, 0, stderr);
        assert.ok(ms < 3000, `exit took ${ms} ms`);
        assert.equal(requests.length, 1);
    });
});

test('a token replaced mid-request lets that request finish instead of resending it', async () => {
    await fixture(async ({ logger, requests, url, handle }) => {
        let first;
        handle((_request, response, count) => {
            if (count === 1) first = response;
            else response.end();
        });
        try {
            logger.log('in flight', { offConsole: true });
            await until(() => requests.length === 1, 1500);
            setApiToken('new token', { url, flushIntervalMs: 20, timeoutMs: 500 });
            logger.log('after', { offConsole: true });
            await delay(60);
            assert.equal(requests.length, 1);
            first.end();
            await until(() => requests.length === 2, 1500);
            await delay(60);
            assert.deepEqual(requests.map(request => request.body), ['GREY: in flight', 'GREY: after']);
            assert.equal(requests[1].headers.authorization, 'Bearer new token');
            assert.notEqual(requests[1].headers['x-justlog-session'], requests[0].headers['x-justlog-session']);
        } finally { first?.end(); }
    }, { timeoutMs: 1000 });
});

test('cloudCycles reached during a slow request sends right after it, not at the next interval', async () => {
    await fixture(async ({ logger, requests, handle }) => {
        let first;
        handle((_request, response, count) => {
            if (count === 1) first = response;
            else response.end();
        });
        try {
            logger.log('one', { offConsole: true });
            await until(() => requests.length === 1, 1500);
            logger.log('two', { offConsole: true });
            first.end();
            await until(() => requests.length === 2, 1000);
            assert.equal(requests[1].body, 'GREY: two');
        } finally { first?.end(); }
    }, { flushIntervalMs: 60000, timeoutMs: 1000 });
});

test('a redirect stops cloud sending with a warning that names the new address', async () => {
    const original = console.error;
    const warnings = [];
    console.error = message => warnings.push(message);
    try {
        await fixture(async ({ logger, requests, handle }) => {
            handle((_request, response) => {
                response.writeHead(301, { Location: 'https://example.com/api/logs/' });
                response.end();
            });
            logger.log('one', { offConsole: true });
            await until(() => warnings.length === 1, 1500);
            logger.log('two', { offConsole: true });
            await delay(80);
            assert.equal(requests.length, 1);
            assert.match(warnings[0], /HTTP 301 to https:\/\/example\.com\/api\/logs\/.*Set url to the final address/);
        });
    } finally { console.error = original; }
});

test('a line logged synchronously by a later beforeExit listener is sent', async () => {
    await fixture(async ({ requests, url }) => {
        const { code, stderr } = await runChild(`import { Logger, setApiToken } from './dist/index.js';
            setApiToken('child', { url: ${JSON.stringify(url)}, flushIntervalMs: 60000, timeoutMs: 1000 });
            const logger = new Logger('child', { file: false, withTime: false });
            process.once('beforeExit', () => logger.log('bye', { offConsole: true }));`);
        assert.equal(code, 0, stderr);
        assert.deepEqual(requests.map(request => request.body), ['GREY: bye']);
    });
});

test('rapid token replacements wait for every earlier sender', async () => {
    await fixture(async ({ logger, requests, url, handle }) => {
        let first;
        handle((_request, response, count) => {
            if (count === 1) first = response;
            else response.end();
        });
        try {
            logger.log('slow', { offConsole: true });
            await until(() => requests.length === 1, 1500);
            // B gives up on its own shutdown after 20 ms; C must still wait for A.
            setApiToken('b', { url, flushIntervalMs: 20, timeoutMs: 20 });
            setApiToken('c', { url, flushIntervalMs: 20, timeoutMs: 500 });
            logger.log('later', { offConsole: true });
            await delay(120);
            assert.equal(requests.length, 1);
            first.end();
            await until(() => requests.length === 2, 1500);
            await delay(60);
            assert.deepEqual(requests.map(request => request.body), ['GREY: slow', 'GREY: later']);
            assert.equal(requests[1].headers.authorization, 'Bearer c');
        } finally { first?.end(); }
    }, { timeoutMs: 1000 });
});

test('shutdown() stays bounded by the newest timeout while an older sender still waits', async () => {
    await fixture(async ({ logger, requests, url, handle }) => {
        let first;
        handle((_request, response, count) => {
            if (count === 1) first = response;
            else response.end();
        });
        try {
            logger.log('stuck', { offConsole: true });
            await until(() => requests.length === 1, 1500);
            setApiToken('b', { url, flushIntervalMs: 20, timeoutMs: 20 });
            const start = Date.now();
            await shutdown();
            assert.ok(Date.now() - start < 500, `shutdown took ${Date.now() - start} ms`);
        } finally { first?.end(); }
    }, { timeoutMs: 3000 });
});
