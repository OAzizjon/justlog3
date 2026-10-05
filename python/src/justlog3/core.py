"""
Repository: https://github.com/OAzizjon/justlog3
Author: Obd. Azizjon
Version: 1.1.0
License: MIT
"""

import gzip
import signal
import uuid
import time as _time
from datetime import datetime
from atexit import register
from platform import system
from sys import exit
import threading
import httpx

_all_loggers = []

global_lock = threading.Lock()

CLOUD_BUF_MAX = 10_000
_cloud = None
_cloud_lock = threading.Lock()

URL = 'https://jl3-cloud.site/api/logs/'

# One request carries at most this much text (the server takes 1 MB as sent
# and 4 MB unpacked); a backlog after an outage goes out in several requests.
MAX_BATCH_BYTES = 512 * 1024
MAX_BATCH_LINES = 5_000
# The server keeps 8 KB of a line; longer ones are cut here to save bandwidth.
MAX_LINE_CHARS = 16 * 1024
# Bodies from this size are sent gzip-compressed (logs shrink 5-10x).
COMPRESS_FROM_BYTES = 1024
# After a network error or a 5xx: wait 5 s, then 10, 20 ... up to 5 minutes.
BACKOFF_START = 5.0
BACKOFF_MAX = 300.0


def _cloud_err(msg : str):
    print(f"{Logger.colors['red']}[JustLog3Error] {msg}{Logger.colors['reset']}", flush=True)


def _retry_after(resp) -> float:
    try:
        return max(1.0, float(resp.headers.get('Retry-After', '60')))
    except ValueError:
        return 60.0


def _take_batch(lines : list, max_lines : int) -> list:
    """The oldest lines that fit into one request (always at least one)."""
    batch = []
    size = 0
    for line in lines:
        if len(line) > MAX_LINE_CHARS:
            line = line[:MAX_LINE_CHARS] + ' [...line truncated]'
        line_size = len(line.encode('utf-8')) + 1
        if batch and (len(batch) >= max_lines or size + line_size > MAX_BATCH_BYTES):
            break
        batch.append(line)
        size += line_size
    return batch


class _CloudSender:
    def __init__(self, token : str, *, url : str = URL, flush_interval : float = 5.0, timeout : float = 5.0):
        self.token = token
        self.url = url
        self.flush_interval = flush_interval
        self.timeout = timeout
        self.session_id = uuid.uuid4().hex
        self.disabled = False
        self._warned = False
        self._seq = 0
        self._seq_lock = threading.Lock()
        self._wake_event = threading.Event()
        self._stop = threading.Event()
        # Sending waits until this time.monotonic(): Retry-After of a 429 or a backoff.
        self._paused_until = 0.0
        self._pause_is_backoff = False
        self._backoff = BACKOFF_START
        self._client = httpx.Client(timeout=timeout)
        self._thread = threading.Thread(target=self._run, name='JustLog3-Cloud', daemon=True)
        self._thread.start()

    def wake(self):
        if not self._wake_event.is_set():
            self._wake_event.set()

    def shutdown(self):
        self._stop.set()
        self._wake_event.set()
        self._thread.join(self.timeout + 1)
        # One last try even during a network backoff; a 429 pause is kept,
        # the server would only refuse again.
        if self._pause_is_backoff:
            self._paused_until = 0.0
        try:
            self._drain(deadline=_time.monotonic() + self.timeout * 2)
        except Exception as e:
            _cloud_err(f"Cloud shutdown drain error: {e}")
        self._client.close()

    def _next_seq(self) -> int:
        with self._seq_lock:
            self._seq += 1
            return self._seq

    def _run(self):
        while True:
            self._wake_event.wait(self.flush_interval)
            self._wake_event.clear()
            if not self.disabled:
                try:
                    self._drain()
                except Exception as e:
                    _cloud_err(f"Cloud worker error: {e}")
            if self._stop.is_set():
                break

    def _drain(self, deadline : float = None):
        for logger in list(_all_loggers):
            try:
                self._drain_logger(logger, deadline)
            except Exception as e:
                _cloud_err(f"Cloud drain error for '{logger.filename}': {e}")

    def _pause(self, seconds : float, backoff : bool):
        self._paused_until = _time.monotonic() + seconds
        self._pause_is_backoff = backoff

    def _drain_logger(self, logger : "Logger", deadline : float = None):
        """Send the logger's backlog, oldest first, until it is empty or sending has to wait."""
        while not self.disabled and _time.monotonic() >= self._paused_until:
            if deadline is not None and _time.monotonic() > deadline:
                return
            with logger.lock:
                if not logger._cloud_buf:
                    return
                retry = logger._cloud_retry
                logger._cloud_retry = None
                if retry and retry[2] == logger._cloud_dropped and len(logger._cloud_buf) >= retry[1]:
                    # The last attempt got no answer but may have been stored:
                    # the same lines under the same Seq let the server skip a repeat.
                    seq, count, _ = retry
                    batch = _take_batch(logger._cloud_buf[:count], count)
                else:
                    seq = self._next_seq()
                    batch = _take_batch(logger._cloud_buf, logger._cloud_batch_lines)
                dropped_at_send = logger._cloud_dropped
            if not self._send(logger, batch, seq, dropped_at_send):
                return

    def _send(self, logger : "Logger", batch : list, seq : int, dropped_at_send : int) -> bool:
        """POST one batch; True if the next one may follow right away."""
        body = '\n'.join(batch).encode('utf-8')
        headers = {
            'Authorization': f'Bearer {self.token}',
            'Content-Type': 'text/plain; charset=utf-8',
            'X-JustLog-Session': self.session_id,
            'X-JustLog-Seq': str(seq),
            'X-JustLog-Logger': logger.filename,
            'X-JustLog-UTC-Offset': str(_time.localtime().tm_gmtoff) if logger.with_time else '',
        }
        if len(body) >= COMPRESS_FROM_BYTES:
            body = gzip.compress(body, compresslevel=6)
            headers['Content-Encoding'] = 'gzip'
        try:
            resp = self._client.post(self.url, content=body, headers=headers)
        except httpx.HTTPError as e:
            self._keep_for_retry(logger, seq, len(batch), dropped_at_send)
            self._warn_once(f"Cloud request failed (no answer), the batch is kept and will be resent: {e}")
            return False

        status = resp.status_code
        if status in (401, 403):
            self.disabled = True
            _cloud_err(f"API token rejected by the server (HTTP {status}). Cloud sending stopped; logs keep being written to files. Check the token and call set_api_token() again.")
            return False
        if status == 429:
            # Rate or daily limit: nothing was stored, lines stay buffered
            # (up to CLOUD_BUF_MAX per logger) until the server takes them again.
            wait = _retry_after(resp)
            self._pause(wait, backoff=False)
            self._warn_once(f"Cloud limit reached (HTTP 429), sending resumes in {int(wait)} s; lines stay buffered.")
            return False
        if status == 413 and len(batch) > 1:
            # Too big for the server: send half as many lines per request from now on.
            logger._cloud_batch_lines = max(1, len(batch) // 2)
            return True
        if status >= 500:
            self._keep_for_retry(logger, seq, len(batch), dropped_at_send)
            self._warn_once(f"Cloud server error (HTTP {status}), the batch is kept and will be resent.")
            return False

        # Stored (2xx), or refused for good (other 4xx): either way these lines are done.
        with logger.lock:
            # An overflow during the request may already have dropped some of them.
            done = max(0, len(batch) - (logger._cloud_dropped - dropped_at_send))
            del logger._cloud_buf[:done]
        self._backoff = BACKOFF_START
        if status >= 400:
            self._warn_once(f"Cloud rejected batch #{seq} (HTTP {status}), batch lost.")
            return True
        rejected = resp.headers.get('X-JustLog-Lines-Rejected')
        if rejected:
            self._warn_once(f"Daily cloud limit reached: {rejected} lines of batch #{seq} were not stored.")
        else:
            self._warned = False
        return True

    def _keep_for_retry(self, logger : "Logger", seq : int, count : int, dropped_at_send : int):
        with logger.lock:
            logger._cloud_retry = (seq, count, dropped_at_send)
        self._pause(self._backoff, backoff=True)
        self._backoff = min(self._backoff * 2, BACKOFF_MAX)

    def _warn_once(self, msg : str):
        if not self._warned:
            self._warned = True
            _cloud_err(msg)


def set_api_token(token : str, *, url : str = URL, flush_interval : float = 5.0, timeout : float = 5.0):
    """
    Enables sending logs to JustLog3 Cloud. Call once; before or after creating loggers, doesn't matter.
    Lines are sent in the background every `flush_interval` seconds (and sooner once
    `cloud_cycles` lines are waiting); bigger backlogs go out compressed in several requests.
    """
    global _cloud
    if not isinstance(token, str) or not token.strip():
        raise ValueError("token must be a non-empty string")
    with _cloud_lock:
        if _cloud is not None:
            _cloud.shutdown()
        _cloud = _CloudSender(token.strip(), url=url, flush_interval=flush_interval, timeout=timeout)
        register(_cloud.shutdown)
    print("[JustLog3] Cloud: log sending enabled.")



class Logger:
    colors = {
                'red' : '\033[31m',
                'green' :  '\033[92m',
                'grey': '\033[90m',
                'reset' : '\033[0m'
            }
    def __init__(self, filename : str = 'app.log', *,  with_time : bool = True, filemode : str = 'a', cycles : int = 50, off_sigmask : bool = False, cloud_cycles : int = 75):
        self.filename = filename if filename.endswith('.log') else f'{filename}.log'
        self.with_time = with_time
        if filemode == 'w':
            with open(self.filename, 'w', encoding='utf-8'):
                pass
            self.filemode = 'a'
        else:
            self.filemode = filemode
        self.cycles = cycles
        self._buffer = []
        self._cloud_buf = []
        # Lines the full cloud buffer pushed out so far; tells a retry whether
        # the lines it sent are still the oldest ones in the buffer.
        self._cloud_dropped = 0
        # (seq, line count, _cloud_dropped) of a batch that got no answer.
        self._cloud_retry = None
        self._cloud_batch_lines = MAX_BATCH_LINES
        self.os = system()
        self.cloud_cycles = cloud_cycles
        self.can_block_SIGTERM = self.os in  ['Linux', 'Darwin'] and not off_sigmask
        self.lock = threading.RLock()
        _all_loggers.append(self)
        register(self.flush)
        _install_signal_handler()

    def try_flush(self): 
        if not self.lock.acquire(blocking=False):
            return
        if self.can_block_SIGTERM:
            signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGTERM})
        self.flush(blocked_SIGTERM=self.can_block_SIGTERM, is_locked = True)
        
    def flush(self, blocked_SIGTERM : bool = False, signum = None, frame = None, is_locked : bool = False):
        if not is_locked:
            self.lock.acquire(blocking=True)
        if not self._buffer:
            if blocked_SIGTERM:
                signal.pthread_sigmask(signal.SIG_UNBLOCK, {signal.SIGTERM})
            self.lock.release()
            return
        try:
            with open(self.filename, self.filemode, encoding='utf-8') as f:
                f.writelines(self._buffer)
        except Exception as e:
            lost_logs = self._buffer.copy() 
            print(f"{self.colors['red']}[JustLog3Error] : {e}\nFailed to write logs to disk, most likely because it is blocked/full\nError time: {datetime.now().isoformat(sep=' ', timespec='milliseconds')}{self.colors['reset']}")
            print(f"Unwritten logs: {lost_logs}", flush=True)
        finally:
            self._buffer.clear()
            if blocked_SIGTERM:
                signal.pthread_sigmask(signal.SIG_UNBLOCK, {signal.SIGTERM})
            self.lock.release()


    def get_time(self):
        if self.with_time:
            now = datetime.now().isoformat(sep=' ', timespec='milliseconds')
            return now + ' -'
        else:
            return ''

    def status(self):
        return {
            'logger_name' : type(self).__name__,
            'path_to_file' : __file__,
            'filename' : self.filename,
            'with_time' : self.with_time,
            'filemode' : self.filemode,
            'cycles' : self.cycles,
            'cloud_cycles' : self.cloud_cycles,
            'os' : self.os,
            'can_block_SIGTERM' : self.can_block_SIGTERM,
        }

    def status_text(self):
        return f"{type(self).__name__}, path: {__file__}\nFilename: {self.filename}\n{'Logger logs with time' if self.with_time else 'Logger logs without time'}\nFilemode: {self.filemode}\nCycles : {self.cycles}\nCloud cycles: {self.cloud_cycles}\nOS: {self.os}\n Can Block SIGTERM (Ctrl + C): {self.can_block_SIGTERM}"

    def log(self, message : str = '[no text]', *, red : bool = False, green : bool = False, custom_prefix : str = None, off_console : bool = False):
        time = self.get_time()
        if red:
            prefix = custom_prefix or 'RED'
            color_code = self.colors['red']
        elif green:
            prefix = custom_prefix or 'GREEN'
            color_code = self.colors['green']
        else:
            prefix = custom_prefix or 'GREY'
            color_code = self.colors['grey']
        file_log = f"{prefix}:{time} {message}"
        if not off_console:
            console_log = f"{color_code}{file_log}{self.colors['reset']}"
            print(console_log)
        cloud = _cloud
        should_wake = False
        with self.lock:
            self._buffer.append(file_log + '\n')
            should_flush = len(self._buffer) >= self.cycles
            if cloud is not None and not cloud.disabled:
                cbuf = self._cloud_buf
                cbuf.append(file_log)
                if len(cbuf) > CLOUD_BUF_MAX:
                    self._cloud_dropped += len(cbuf) - CLOUD_BUF_MAX
                    del cbuf[:-CLOUD_BUF_MAX]
                    if not getattr(self, '_cloud_buf_warned', False):
                        self._cloud_buf_warned = True
                        _cloud_err(f"Cloud buffer for '{self.filename}' overflowed (>{CLOUD_BUF_MAX}), oldest lines dropped.")
                should_wake = len(cbuf) >= self.cloud_cycles
        if should_flush:
            self.try_flush()
        if should_wake:
            cloud.wake()


class BasicLogger(Logger):
    levels = {
        'NOTSET' : 0,
        'DEBUG' : 1,
        'INFO' : 2,
        'WARNING' : 3,
        'ERROR' : 4,
        'SUCCESS' : 4,
        'CRITICAL' : 5
    } 
    def __init__(self, filename = 'app.log', *, with_time = True, filemode = 'a', cycles = 50, off_sigmask = False, cloud_cycles : int = 75, console_level : str = 'DEBUG'):
        super().__init__(filename, with_time=with_time, filemode=filemode, cycles=cycles, off_sigmask=off_sigmask, cloud_cycles=cloud_cycles)
        if console_level.upper() not in self.levels:
            self.log(f"Invalid value '{console_level}' for console_level. Valid options are:\nNOTSET, DEBUG, INFO, WARNING, ERROR / SUCCESS, CRITICAL", red=True, custom_prefix='[JustLog3Error]')
            console_level = 'DEBUG'
        self.console_level = console_level.upper()
    def _check_level(self, level : str = 'NOTSET'):
        return self.levels[self.console_level] > self.levels[level]

    def debug(self, message : str):
        self.log(message, custom_prefix='DEBUG', off_console=self._check_level('DEBUG'))

    def info(self, message : str):
        self.log(message, green=True, custom_prefix='INFO', off_console=self._check_level('INFO'))

    def warning(self, message : str):
        self.log(message, red=True, custom_prefix='WARNING', off_console=self._check_level('WARNING'))

    def error(self, message : str):
        self.log(message, red=True, custom_prefix='ERROR', off_console=self._check_level('ERROR'))

    def success(self, message : str):
        self.log(message, green=True, custom_prefix='SUCCESS', off_console=self._check_level('SUCCESS'))

    def critical(self, message : str):
        self.log(message, red=True, custom_prefix='CRITICAL', off_console=self._check_level('CRITICAL'))

_signal_handling_enabled = True  
_signal_installed = False
_install_lock = threading.Lock()

def off_signal_handler():
    with _install_lock:
        global _signal_handling_enabled
        if _signal_installed:
                print(f"[JustLog3Error] WARNING! The SIGTERM handler is ALREADY installed — calling disable_signal_handling() now has no effect. Call it right after import, before creating any loggers, on the very next line!", flush=True)
                return
        _signal_handling_enabled = False
        print(f"[JustLog3] SIGTERM interception disabled. Call justlog.shutdown() or flush() on each logger manually before the process exits.")

def _install_signal_handler():
    with _install_lock:
        global _signal_installed
        if _signal_installed or not _signal_handling_enabled: 
            return
        try:
            signal.signal(signal.SIGTERM, send_signal)
            _signal_installed = True
            print(f"[JustLog3] SIGTERM interception ENABLED — log buffers will be flushed automatically when the signal is received.")
        except Exception as e:
            print(f'[JustLog3Error] Logger was imported from outside the main thread: {e}', flush=True)
            


def shutdown():
    for logger in _all_loggers:
        logger.flush()
    if _cloud is not None:
        _cloud.shutdown()

def send_signal(signum, frame):
    if not global_lock.acquire(blocking=False):
        return
    exit(0)