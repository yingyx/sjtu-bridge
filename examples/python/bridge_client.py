"""Dependency-free client example using sjtu's JSON CLI, never vault/token data.

Example (connection file is optional for public discovery):
  python bridge_client.py --sjtu /absolute/path/sjtu services --query 校历
  python bridge_client.py --sjtu /absolute/path/sjtu --connection /private/app.json capabilities
  python bridge_client.py --sjtu /absolute/path/sjtu --connection /private/app.json download REF --output /private/new-file

For business calls, discover the CLI route and input schema first, then pass the
route as an argv list to invoke(). JSON goes through stdin, never shell text.
"""

import argparse
import json
import os
import subprocess
import sys


class BridgeError(Exception):
    def __init__(self, error):
        self.error = error
        super().__init__(error.get("code", "CLI_FAILED") + ": " + error.get("message", "Call failed"))


class Bridge:
    def __init__(self, executable, connection=None):
        if not os.path.isabs(executable) or (connection and not os.path.isabs(connection)):
            raise ValueError("Executable and connection paths must be absolute")
        self.executable, self.connection = executable, connection

    def call(self, route, *, data=None, timeout=30):
        args = [self.executable, *route, "--json"]
        if self.connection:
            args.extend(["--connection", self.connection])
        if data is not None:
            args.append("--input-json-stdin")
        try:
            result = subprocess.run(
                args, input=json.dumps(data, ensure_ascii=False) if data is not None else None,
                capture_output=True, text=True, encoding="utf-8", timeout=timeout, check=False,
            )
        except subprocess.TimeoutExpired as exc:
            # Killing a child may leave its local output file; inspect/remove it
            # before retrying. Prefer the SDK for cooperative transfer cancellation.
            raise BridgeError({"code": "TIMEOUT", "message": "CLI deadline exceeded; inspect partial output"}) from exc
        except OSError as exc:
            raise BridgeError({"code": "CLI_UNAVAILABLE", "message": "Cannot launch sjtu"}) from exc
        if result.returncode:
            try:
                error = json.loads(result.stderr)["error"]
            except (ValueError, KeyError, TypeError):
                error = {"code": "CLI_FAILED", "message": "CLI returned an invalid error response"}
            raise BridgeError(error)
        try:
            return json.loads(result.stdout)
        except ValueError as exc:
            raise BridgeError({"code": "RESPONSE_INCOMPLETE", "message": "CLI returned invalid JSON"}) from exc

    def invoke(self, route, data):
        return self.call(route, data=data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sjtu", required=True)
    parser.add_argument("--connection")
    commands = parser.add_subparsers(dest="command", required=True)
    services = commands.add_parser("services")
    services.add_argument("--query", default="")
    commands.add_parser("capabilities")
    download = commands.add_parser("download")
    download.add_argument("ref")
    download.add_argument("--output", required=True)
    args = parser.parse_args()
    bridge = Bridge(args.sjtu, args.connection)
    if args.command == "services":
        result = bridge.call(["services", "search", args.query] if args.query else ["services", "list"])
    elif args.command == "capabilities":
        result = bridge.call(["capabilities", "list"])
    else:
        result = bridge.call(["content", "download", args.ref, "--output", args.output], timeout=300)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    # Match the CLI encoding even when Windows redirects this process's output.
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")
    try:
        main()
    except BridgeError as exc:
        print(json.dumps({"error": exc.error}, ensure_ascii=False), file=sys.stderr)
        sys.exit(1)
