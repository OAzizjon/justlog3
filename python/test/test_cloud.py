"""Cloud sending against a local fake server: python test/test_cloud.py (from the package folder)."""

import gzip
import os
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

import justlog3  # noqa: E402
from justlog3 import core  # noqa: E402

justlog3.off_signal_handler()


class FakeServer:
    """Records every request; answers from a script of (status, headers) or 'drop'."""

    def __init__(self):
        self.requests = []
        self.script = []
        server = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                raw = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                body = gzip.decompress(raw) if self.headers.get('Content-Encoding') == 'gzip' else raw
                server.requests.append({
                    'headers': dict(self.headers),
                    'raw_size': len(raw),
                    'lines': body.decode('utf-8').split('\n'),
                })
                answer = server.script.pop(0) if server.script else (202, {})
                if answer == 'drop':
                    # The request arrived, the answer never does (lost response).
                    self.close_connection = True
                    return
                status, headers = answer
                self.send_response(status)
                for name, value in headers.items():
                    self.send_header(name, value)
                self.send_header('Content-Length', '0')
                self.end_headers()

        self.httpd = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.url = f'http://127.0.0.1:{self.httpd.server_address[1]}/api/logs/'
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


class CloudSendingTestCase(unittest.TestCase):
    def setUp(self):
        self.server = FakeServer()
        self.folder = tempfile.TemporaryDirectory()
        # The background thread never wakes on its own; tests drain by hand.
        justlog3.set_api_token('token', url=self.server.url, flush_interval=3600)
        self.cloud = core._cloud
        self.logger = justlog3.Logger(os.path.join(self.folder.name, 'test.log'), cloud_cycles=10 ** 9)

    def tearDown(self):
        self.cloud.disabled = True
        self.cloud._stop.set()
        self.cloud._wake_event.set()
        self.cloud._thread.join()
        self.cloud._client.close()
        core._cloud = None
        core._all_loggers.clear()
        self.logger._buffer.clear()
        self.server.close()
        self.folder.cleanup()

    def write(self, count, width=100):
        for number in range(count):
            self.logger.log(f'line {number:06d} ' + 'x' * width, off_console=True)

    def drain(self):
        self.cloud._paused_until = 0.0
        self.cloud._drain()

    def test_big_backlog_goes_out_compressed_in_several_requests(self):
        self.write(12_000)

        self.drain()

        requests = self.server.requests
        self.assertGreater(len(requests), 1)
        self.assertTrue(all(request['headers'].get('Content-Encoding') == 'gzip' for request in requests))
        self.assertTrue(all(sum(len(line) + 1 for line in request['lines']) <= core.MAX_BATCH_BYTES for request in requests))
        lines = [line for request in requests for line in request['lines']]
        # The buffer keeps the newest CLOUD_BUF_MAX lines; all of them arrive once, in order.
        self.assertEqual(len(lines), core.CLOUD_BUF_MAX)
        self.assertIn('line 002000', lines[0])
        self.assertIn('line 011999', lines[-1])
        seqs = [request['headers']['X-JustLog-Seq'] for request in requests]
        self.assertEqual(len(set(seqs)), len(seqs))
        self.assertEqual(self.logger._cloud_buf, [])

    def test_unanswered_batch_is_resent_with_the_same_seq(self):
        self.server.script = ['drop']
        self.write(3)

        self.drain()
        self.assertEqual(len(self.logger._cloud_buf), 3)
        self.write(2)
        self.drain()

        first, retry, rest = self.server.requests
        self.assertEqual(first['headers']['X-JustLog-Seq'], retry['headers']['X-JustLog-Seq'])
        self.assertEqual(first['lines'], retry['lines'])
        self.assertEqual(len(rest['lines']), 2)
        self.assertNotEqual(rest['headers']['X-JustLog-Seq'], retry['headers']['X-JustLog-Seq'])
        self.assertEqual(self.logger._cloud_buf, [])

    def test_429_keeps_the_lines_and_waits_for_retry_after(self):
        self.server.script = [(429, {'Retry-After': '120'})]
        self.write(4)

        self.cloud._drain()
        self.cloud._drain()

        self.assertEqual(len(self.server.requests), 1)
        self.assertEqual(len(self.logger._cloud_buf), 4)
        self.assertGreater(self.cloud._paused_until - time.monotonic(), 100)
        self.drain()
        self.assertEqual(self.logger._cloud_buf, [])

    def test_413_halves_the_batch(self):
        self.server.script = [(413, {})]
        self.write(10)

        self.drain()

        self.assertEqual([len(request['lines']) for request in self.server.requests], [10, 5, 5])
        self.assertEqual(self.logger._cloud_buf, [])

    def test_server_error_keeps_the_batch_and_backs_off(self):
        self.server.script = [(503, {})]
        self.write(2)

        self.cloud._drain()

        self.assertEqual(len(self.logger._cloud_buf), 2)
        self.assertGreater(self.cloud._paused_until, time.monotonic())
        self.drain()
        self.assertEqual(self.logger._cloud_buf, [])
        self.assertEqual(
            self.server.requests[0]['headers']['X-JustLog-Seq'],
            self.server.requests[1]['headers']['X-JustLog-Seq'],
        )

    def test_partially_stored_batch_is_done(self):
        self.server.script = [(202, {'X-JustLog-Lines-Rejected': '3'})]
        self.write(5)

        self.drain()

        self.assertEqual(len(self.server.requests), 1)
        self.assertEqual(self.logger._cloud_buf, [])

    def test_small_batches_are_sent_uncompressed(self):
        self.write(1, width=10)

        self.drain()

        self.assertNotIn('Content-Encoding', self.server.requests[0]['headers'])


if __name__ == '__main__':
    unittest.main()
