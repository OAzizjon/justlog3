"""justlog3 vs logging vs loguru: wall time per call, final flush included.

    python bench.py [--n 50000] [--runs 7] [--only justlog3] [--lib PATH]

By default it benchmarks the justlog3 source in this repository (../../python/src),
not an installed release. loguru comes from requirements.txt.
"""
import argparse
import gc
import http.server
import logging
import os
import platform
import socket
import statistics
import subprocess
import sys
import tempfile
import threading
import time

HERE = os.path.dirname(os.path.abspath(__file__))

parser = argparse.ArgumentParser()
parser.add_argument('--lib', default=os.path.join(HERE, '..', '..', 'python', 'src'))
parser.add_argument('--n', type=int, default=50_000)
parser.add_argument('--runs', type=int, default=7)
parser.add_argument('--only')
args = parser.parse_args()
sys.path.insert(0, os.path.abspath(args.lib))

import justlog3  # noqa: E402
from justlog3 import core  # noqa: E402

justlog3.off_signal_handler()
MESSAGE = 'user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms'
DEVNULL = open(os.devnull, 'w', encoding='utf-8')


def run_threads(fn, n, threads):
    # The first n % threads workers take one call more, so exactly n calls run.
    counts = [n // threads + (i < n % threads) for i in range(threads)]
    workers = [threading.Thread(target=lambda count=count: [fn() for _ in range(count)]) for count in counts]
    for w in workers:
        w.start()
    for w in workers:
        w.join()


# --- setups: each returns (log_one, finish) ---------------------------------------

def setup_justlog3(path, console, cloud_url=None):
    if cloud_url:
        justlog3.set_api_token('bench', url=cloud_url)
    logger = justlog3.Logger(path)
    off = not console

    def finish():
        justlog3.shutdown()
        # Forget this run's logger and sender, so the next run starts from scratch
        # and shutdown() doesn't flush loggers of earlier runs inside the timer.
        core._cloud = None
        core._all_loggers.clear()
    return (lambda: logger.log(MESSAGE, off_console=off)), finish


def setup_logging(path, console):
    log = logging.getLogger(f'bench{time.perf_counter_ns()}')
    log.handlers.clear()
    log.propagate = False
    log.setLevel(logging.INFO)
    fmt = logging.Formatter('%(levelname)s:%(asctime)s - %(message)s')
    handlers = [logging.FileHandler(path, encoding='utf-8')]
    if console:
        handlers.append(logging.StreamHandler(sys.stdout))
    for handler in handlers:
        handler.setFormatter(fmt)
        log.addHandler(handler)

    def finish():
        for handler in handlers:
            handler.close()
            log.removeHandler(handler)
    return (lambda: log.info(MESSAGE)), finish


def setup_loguru(path, console):
    from loguru import logger
    logger.remove()
    fmt = '{level}:{time:YYYY-MM-DD HH:mm:ss.SSS} - {message}'
    logger.add(path, format=fmt, level='INFO')
    if console:
        logger.add(sys.stdout, format=fmt, level='INFO', colorize=True)

    def finish():
        logger.remove()
    return (lambda: logger.info(MESSAGE)), finish


SETUPS = {'justlog3': setup_justlog3, 'logging': setup_logging, 'loguru': setup_loguru}


def measure(name, console=False, threads=1, cloud_url=None):
    """Seconds of one run: setup excluded, logging and the final flush/close included."""
    folder = tempfile.mkdtemp()
    path = os.path.join(folder, 'bench.log')
    real_stdout = sys.stdout
    # "Console" goes to the null device: a real terminal would dominate the timing.
    sys.stdout = DEVNULL
    try:
        if cloud_url:
            log_one, finish = SETUPS[name](path, console, cloud_url)
        else:
            log_one, finish = SETUPS[name](path, console)
        gc.collect()
        start = time.perf_counter()
        if threads == 1:
            for _ in range(args.n):
                log_one()
        else:
            run_threads(log_one, args.n, threads)
        finish()
        elapsed = time.perf_counter() - start
    finally:
        sys.stdout = real_stdout
    with open(path, encoding='utf-8') as file:
        lines = sum(1 for _ in file)
    assert lines == args.n, f'{name}: {lines} lines written, want {args.n}'
    os.remove(path)
    os.rmdir(folder)
    return elapsed


class Sink(http.server.BaseHTTPRequestHandler):
    """A fake JustLog3 server: reads the batch and answers 202."""

    def log_message(self, *a):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        self.send_response(202)
        self.send_header('Content-Length', '0')
        self.end_headers()


if os.environ.get('BENCH_SINK'):
    # Child process: the fake server runs apart so it doesn't share the GIL with the benchmark.
    http.server.ThreadingHTTPServer(('127.0.0.1', int(os.environ['BENCH_SINK'])), Sink).serve_forever()


def main():
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        sink_port = probe.getsockname()[1]
    sink = subprocess.Popen([sys.executable, __file__], env={**os.environ, 'BENCH_SINK': str(sink_port)})
    time.sleep(1.0)
    cloud_url = f'http://127.0.0.1:{sink_port}/api/logs/'

    scenarios = [
        ('File only', {}),
        ('File + console', {'console': True}),
        ('File only, 8 threads', {'threads': 8}),
        ('File + cloud (local server)', {'cloud_url': cloud_url}),
    ]
    names = [args.only] if args.only else list(SETUPS)
    try:
        import loguru
        loguru_version = loguru.__version__
    except ImportError:
        loguru_version = 'not installed'
    print(f'Python {sys.version.split()[0]} on {platform.platform()}; {platform.processor() or platform.machine()}, '
          f'{os.cpu_count()} logical CPUs')
    print(f'justlog3 from {os.path.relpath(args.lib, HERE)}; loguru {loguru_version}')
    print(f'{args.n:,} calls per run, {args.runs} runs, libraries interleaved within each round; '
          f'median (min-max), microseconds per call', end='\n\n')
    print('| Scenario | ' + ' | '.join(names) + ' |')
    print('|---|' + '---|' * len(names))
    try:
        for title, options in scenarios:
            times = {name: [] for name in names}
            for _ in range(args.runs):
                for name in names:
                    # Only justlog3 sends to the cloud.
                    if 'cloud_url' in options and name != 'justlog3':
                        continue
                    times[name].append(measure(name, **options) / args.n * 1e6)
            cells = []
            for name in names:
                values = times[name]
                cells.append(f'{statistics.median(values):.1f} ({min(values):.1f}-{max(values):.1f})' if values else '-')
            print(f'| {title} | ' + ' | '.join(cells) + ' |', flush=True)
    finally:
        sink.kill()


main()
