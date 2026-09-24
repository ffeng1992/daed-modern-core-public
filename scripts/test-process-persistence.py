#!/usr/bin/env python3
"""Cold-process API persistence and SQLite backup restoration, synthetic only.
This does not validate datapath activation, browser UI or old-binary rollback.
"""
import json
import pathlib
import socket
import sqlite3
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

binary = pathlib.Path(sys.argv[1]).resolve()
live = "--datapath" in sys.argv[2:]
candidate_binary = binary
original_binary = pathlib.Path(sys.argv[sys.argv.index("--original") + 1]).resolve() if "--original" in sys.argv else None
if original_binary:
    assert live
    binary = original_binary
with tempfile.TemporaryDirectory(prefix="bridge-process-") as tmp:
    root = pathlib.Path(tmp)
    config = root / "config"
    config.mkdir()
    original_config = config
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    url = f"http://127.0.0.1:{port}/graphql"
    token = ""
    process = None
    log = open(root / "daemon.log", "wb")

    def query(text, authenticated=True, expected_error=False):
        headers = {"Content-Type": "application/json"}
        if authenticated:
            headers["Authorization"] = "Bearer " + token
        req = urllib.request.Request(url, json.dumps({"query": text}).encode(), headers)
        with urllib.request.urlopen(req, timeout=90) as response:
            result = json.load(response)
        if bool(result.get("errors")) != expected_error:
            raise RuntimeError("Unexpected GraphQL error state; synthetic logs retained until process cleanup")
        return result.get("data", {})

    def start():
        global process
        process = subprocess.Popen([str(binary), "run", *([] if live else ["--api-only"]), "--config", str(config), "--listen", f"127.0.0.1:{port}"], stdout=log, stderr=log)
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError("daemon exited before readiness")
            try:
                query("{configs{id}}", authenticated=False, expected_error=True)
                return
            except (OSError, urllib.error.URLError):
                time.sleep(.1)
        raise TimeoutError("API startup timeout")

    def stop():
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
                raise RuntimeError("daemon did not terminate within 10 seconds")

    def check_dns():
        if not live:
            return
        packet = struct.pack("!HHHHHH", 0x1234, 0x100, 1, 0, 0, 0) + b"\x07fixture\x07invalid\0" + struct.pack("!HH", 1, 1)
        for kind in (socket.SOCK_DGRAM, socket.SOCK_STREAM):
            with socket.socket(socket.AF_INET, kind) as client:
                client.settimeout(3)
                client.connect(("127.0.0.1", 15363))
                if kind == socket.SOCK_DGRAM:
                    client.send(packet)
                    answer = client.recv(4096)
                else:
                    client.sendall(struct.pack("!H", len(packet)) + packet)
                    def receive(count):
                        data = b""
                        while len(data) < count:
                            chunk = client.recv(count - len(data))
                            assert chunk, "incomplete TCP DNS"
                            data += chunk
                        return data
                    answer = receive(struct.unpack("!H", receive(2))[0])
                assert len(answer) >= 12 and answer[:2] == b"\x12\x34" and answer[2] & 0x80, (
                    "DNS listener not serving: phase=" + ("official" if binary == original_binary else "candidate")
                    + " transport=" + ("udp" if kind == socket.SOCK_DGRAM else "tcp")
                    + " length=" + str(len(answer)) + " synthetic_header=" + answer[:12].hex()
                )

    def configure_live():
        if not live:
            return
        definitions = {
            "Config": 'global:{disableWaitingNetwork:true}' if original_binary else 'global:{disableWaitingNetwork:true,bpfConnStateMapSize:2048}',
            "Dns": 'dns:' + json.dumps("bind: 'tcp+udp://127.0.0.1:15363'\nipversion_prefer: 4\nrouting { request { fallback: reject } response { fallback: accept } }"),
            "Routing": 'routing:"fallback: direct"',
        }
        for kind, value in definitions.items():
            result = query('mutation{create'+kind+'(name:"fixture",'+value+'){id}}')
            identity = json.dumps(result['create'+kind]['id'])
            query('mutation{select'+kind+'(id:'+identity+')}')
        query('mutation{run(dry:false)}')
        check_dns()

    try:
        start()
        token = query('mutation{createUser(username:"fixture",password:"Synthetic12345")}', False)["createUser"]
        query('mutation{createGroup(name:"persisted",policy:fixed,policyParams:[{val:"0"}]){id}}')
        expected = query("{groups{id name policy}}")
        configure_live()
        stop()
        if original_binary:
            old_db = list(original_config.glob("*.db"))
            assert len(old_db) == 1
            config = root / "candidate"
            config.mkdir()
            with sqlite3.connect(old_db[0]) as src, sqlite3.connect(config / old_db[0].name) as dst:
                src.backup(dst)
            binary = candidate_binary

        start()
        assert query("{groups{id name policy}}") == expected, "cold restart lost data"
        check_dns()
        query('mutation{createUser(username:"other",password:"Synthetic12345")}', False, True)
        stop()
        databases = list(config.glob("*.db"))
        assert len(databases) == 1, "unexpected DB layout"
        db = databases[0]
        backup = root / "backup.db"
        with sqlite3.connect(db) as src, sqlite3.connect(backup) as dst:
            src.backup(dst)
        start()
        query('mutation{createGroup(name:"discarded",policy:fixed,policyParams:[{val:"0"}]){id}}')
        assert len(query("{groups{id}}")['groups']) == 2
        stop()
        with sqlite3.connect(backup) as src, sqlite3.connect(db) as dst:
            src.backup(dst)
        start()
        assert query("{groups{id name policy}}") == expected, "backup restoration failed"
        check_dns()
        if live:
            query('mutation{run(dry:true)}')
            for kind in (socket.SOCK_DGRAM, socket.SOCK_STREAM):
                with socket.socket(socket.AF_INET, kind) as released:
                    released.bind(("127.0.0.1",15363))
        if original_binary:
            stop()
            binary = original_binary
            config = original_config
            start()
            assert query("{groups{id name policy}}") == expected, "original data was changed"
            check_dns()
            print("PASS: official original -> isolated candidate -> original; preserved separate database and real UDP/TCP DNS")
        print("PASS: actual process restart, auth persistence, consistent SQLite backup and restore; datapath=" + str(live))
    finally:
        stop()
        log.close()
