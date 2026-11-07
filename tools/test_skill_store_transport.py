"""Offline regression tests: signed bytes and endpoint must survive transport."""
import base64
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("store_transport", Path(__file__).with_name("skill-store-transport.py"))
transport = importlib.util.module_from_spec(spec)
spec.loader.exec_module(transport)


class Response:
    status_code = 200
    raw = type("Raw", (), {"headers": {}})()

    def __enter__(self): return self
    def __exit__(self, *_): pass
    def iter_content(self, _): yield b"synthetic response"


class Session:
    def __init__(self): self.calls = []; self.proxies = {}
    def request(self, method, url, **kwargs):
        self.calls.append((method, url, kwargs))
        return Response()
    def close(self): pass


class SignedTransportTests(unittest.TestCase):
    def test_exact_signed_body_endpoint_and_headers_are_preserved(self):
        # Quotes, unicode, whitespace and a noncanonical XML layout are signed
        # bytes. None may be parsed/re-serialized by the transport layer.
        body = '<plist>\n<string>测试 &quot; \' </string>\n</plist>'.encode()
        for url in ("https://auth.itunes.apple.com/auth/v1/native/fast/", "https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/authenticate", "https://fpinit.itunes.apple.com/v1/signSapSetup/legacy", "https://s.mzstatic.com/sap/setupCert.plist"):
            with self.subTest(url=url):
                session = Session()
                message = {"method": "POST", "url": url, "headers": {"X-Apple-ActionSignature": "synthetic-signature", "Cookie": "synthetic-cookie"}, "body": base64.b64encode(body).decode()}
                output = io.StringIO()
                with patch.object(transport.requests, "Session", return_value=session) as factory, patch.object(transport.sys, "stdin", io.StringIO(json.dumps(message))), contextlib.redirect_stdout(output):
                    transport.main()
                self.assertEqual(factory.call_count, 1, "transport fetched a different bag")
                self.assertEqual(len(session.calls), 1)
                method, actual_url, kwargs = session.calls[0]
                self.assertEqual((method, actual_url, kwargs["data"]), ("POST", url, body))
                self.assertEqual(kwargs["headers"]["X-Apple-ActionSignature"], "synthetic-signature")
                self.assertEqual(kwargs["headers"]["Cookie"], "synthetic-cookie")
                self.assertFalse(kwargs["allow_redirects"])
                self.assertEqual(json.loads(output.getvalue())["status"], 200)

    def test_rejects_foreign_endpoint_before_sending_credentials(self):
        for url in ("https://example.test/", "http://buy.itunes.apple.com/", "https://buy.itunes.apple.com:444/", "https://user@buy.itunes.apple.com/"):
            with self.subTest(url=url), patch.object(transport.requests, "Session") as factory, patch.object(transport.sys, "stdin", io.StringIO(json.dumps({"url": url}))):
                with self.assertRaises(ValueError): transport.main()
                factory.assert_not_called()


if __name__ == "__main__":
    unittest.main()
