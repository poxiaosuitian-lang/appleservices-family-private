"""Direct HTTPS transport for the purchase skill; payloads stay on private pipes."""
import base64
import json
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit

import requests


PROXY_MARKER = Path(__file__).resolve().parents[1] / ".cache" / "store-proxy-10808.enabled"


def configure_transport(session):
    session.trust_env = False
    if PROXY_MARKER.is_file():
        proxy = "http://127.0.0.1:10808"
        session.proxies.update({"http": proxy, "https": proxy})


def main():
    message = json.load(sys.stdin)
    endpoint = urlsplit(message["url"])
    allowed = endpoint.hostname in {
        "init.itunes.apple.com", "auth.itunes.apple.com",
        "buy.itunes.apple.com", "speedysub.itunes.apple.com",
        "fpinit.itunes.apple.com", "s.mzstatic.com"
    } or re.fullmatch(r"p[0-9]+-buy\.itunes\.apple\.com", endpoint.hostname or "")
    if endpoint.scheme != "https" or not allowed or endpoint.username or endpoint.fragment or endpoint.port not in (None, 443):
        raise ValueError("unsupported endpoint")
    headers = message["headers"]
    headers.pop("Content-Length", None)
    headers.pop("Accept-Encoding", None)
    data = base64.b64decode(message["body"] or "")
    # Transport must preserve the exact URL and bytes signed by the caller.
    # Re-encoding XML or fetching a new bag invalidates that signature binding.
    request_url = message["url"]
    session = requests.Session()
    configure_transport(session)
    with session.request(
        message["method"], request_url, headers=headers,
        data=data, allow_redirects=False,
        timeout=20, stream=True,
    ) as response:
        parts = []
        length = 0
        for part in response.iter_content(16384):
            length += len(part)
            if length > 2 * 1024 * 1024:
                raise ValueError("response too large")
            parts.append(part)
        result_headers = {
            name: response.raw.headers.getlist(name) for name in response.raw.headers
            if name.lower() not in {"content-length", "content-encoding", "transfer-encoding"}
        }
        json.dump({
            "status": response.status_code, "headers": result_headers,
            "body": base64.b64encode(b"".join(parts)).decode("ascii"),
        }, sys.stdout)
    session.close()


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print('{"error":"direct-https-transport-failed"}')
        sys.exit(1)
