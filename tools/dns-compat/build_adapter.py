"""Transform a reviewed site adapter without embedding private site settings."""
import hashlib
import pathlib
import sys

SOURCE_SHA256 = '75b75871f6c480347b91dbc007fdd749696814ee9776900f8e1604d3fe191c2b'

def build(source):
    if hashlib.sha256(source).hexdigest() != SOURCE_SHA256:
        raise ValueError('site adapter changed; review before generating')
    s = source.decode()
    s = s.replace('import asyncio, socket, struct, secrets',
                  'import asyncio, socket, struct, secrets\nfrom dns_transport import TCPPool\nCORE_POOL = TCPPool("127.0.0.1", 5353)')
    s = s.replace('async def upstream(q,port):\n',
                  'async def upstream(q,port):\n if port==5353:return await CORE_POOL.exchange(q)\n')
    s = s.replace('  for _ in range(32):\n', '  while True:\n')
    # DNS-over-TCP permits a peer to pipeline queries. An arbitrary 32-query
    # close discarded queued frames. Idle timeout and systemd memory limits stay.
    return s

if __name__ == '__main__':
    pathlib.Path(sys.argv[2]).write_text(build(pathlib.Path(sys.argv[1]).read_bytes()))
