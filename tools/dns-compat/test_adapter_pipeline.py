"""Set SITE_ADAPTER_PATH to a generated adapter; private settings stay outside git."""
import asyncio
import importlib.util
import os
import unittest
from unittest.mock import patch
from test_transport import query


@unittest.skipUnless(os.environ.get('SITE_ADAPTER_PATH'), 'requires reviewed site fixture')
class PipelineTests(unittest.IsolatedAsyncioTestCase):
    async def test_200_pipelined_requests_remain_readable(self):
        spec = importlib.util.spec_from_file_location('site_adapter', os.environ['SITE_ADAPTER_PATH'])
        adapter = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(adapter)
        async def answer(q):
            return q[:2] + b'\x81\x80' + q[4:]
        with patch.object(adapter, 'resolve', side_effect=answer):
            server = await asyncio.start_server(adapter.tcp, '127.0.0.1', 0)
            reader, writer = await asyncio.open_connection('127.0.0.1', server.sockets[0].getsockname()[1])
            try:
                qs = [query(i) for i in range(200)]
                writer.write(b''.join(len(q).to_bytes(2,'big')+q for q in qs))
                await writer.drain()
                async with asyncio.timeout(3):
                    for q in qs:
                        n = int.from_bytes(await reader.readexactly(2),'big')
                        r = await reader.readexactly(n)
                        self.assertEqual(r, await answer(q))
            finally:
                writer.close()
                await writer.wait_closed()
                server.close()
                await server.wait_closed()
                await asyncio.sleep(.02)

if __name__ == '__main__':
    unittest.main()
