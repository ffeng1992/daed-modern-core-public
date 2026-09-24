import asyncio
import struct
import unittest
from dns_transport import TCPPool


def query(i=1, name=b'fixture'):
    return struct.pack('!HHHHHH', i, 256, 1, 0, 0, 0) + bytes([len(name)]) + name + b'\x04test\0\0\1\0\1'


class TransportTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.connections = 0
        self.live = set()
        self.mode = 'normal'
        self.requests = 0
        self.server = await asyncio.start_server(self.serve, '127.0.0.1', 0)
        self.pool = TCPPool('127.0.0.1', self.server.sockets[0].getsockname()[1], 4, .3)

    async def serve(self, reader, writer):
        self.connections += 1
        self.live.add(writer)
        try:
            while True:
                size = int.from_bytes(await reader.readexactly(2), 'big')
                q = await reader.readexactly(size)
                self.requests += 1
                if self.mode == 'silent':
                    await reader.read()
                    return
                if self.mode == 'reset':
                    return
                r = q[:2] + b'\x81\x80' + q[4:]
                if self.mode == 'wrong_id':
                    r = b'\xff\xff' + r[2:]
                if self.mode == 'wrong_name':
                    r = r[:13] + b'XXXXXXX' + r[20:]
                if self.mode == 'short':
                    r = b'ab'
                writer.write(len(r).to_bytes(2, 'big') + r)
                await writer.drain()
                if self.mode == 'one_query':
                    return
        except (asyncio.IncompleteReadError, ConnectionError):
            pass
        finally:
            writer.close()
            await writer.wait_closed()
            self.live.discard(writer)

    async def asyncTearDown(self):
        await self.pool.close()
        self.server.close()
        await self.server.wait_closed()
        for w in list(self.live):
            w.close()
        await asyncio.sleep(.01)

    async def test_sequential_reuses_connection(self):
        for i in range(200):
            r = await self.pool.exchange(query(i))
            self.assertEqual(r[:2], i.to_bytes(2, 'big'))
        self.assertEqual(self.connections, 1)

    async def test_concurrent_same_id_different_question(self):
        qs = [query(1, ('name'+str(i)).encode()) for i in range(200)]
        rs = await asyncio.gather(*(self.pool.exchange(q) for q in qs))
        self.assertTrue(all(q[12:] == r[12:] for q, r in zip(qs, rs)))
        self.assertLessEqual(self.connections, 4)

    async def test_upstream_closes_after_every_query(self):
        self.mode = 'one_query'
        for i in range(60):
            await self.pool.exchange(query(i))
        self.assertEqual(self.requests, 60)

    async def test_invalid_responses_are_not_retried(self):
        for mode in ('wrong_id', 'wrong_name', 'short'):
            self.mode = mode
            before = self.requests
            with self.assertRaises(ValueError):
                await self.pool.exchange(query())
            self.assertEqual(self.requests, before+1)
        self.mode = 'normal'
        await self.pool.exchange(query())

    async def test_deadline_and_recovery(self):
        self.mode = 'silent'
        rs = await asyncio.gather(*(self.pool.exchange(query(i)) for i in range(12)), return_exceptions=True)
        self.assertTrue(all(isinstance(r, TimeoutError) for r in rs))
        self.assertEqual(self.pool.slots.qsize(), 4)
        self.mode = 'normal'
        await self.pool.exchange(query())

    async def test_cancel_returns_slot(self):
        self.mode = 'silent'
        t = asyncio.create_task(self.pool.exchange(query()))
        await asyncio.sleep(.03)
        t.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await t
        self.assertEqual(self.pool.slots.qsize(), 4)
        self.mode = 'normal'
        await self.pool.exchange(query())

    async def test_reset_retry_is_bounded(self):
        self.mode = 'reset'
        with self.assertRaises(asyncio.IncompleteReadError):
            await self.pool.exchange(query())
        self.assertEqual(self.requests, 2)
        self.assertEqual(self.pool.slots.qsize(), 4)

    async def test_all_slots_recover_after_listener_restart(self):
        await asyncio.gather(*(self.pool.exchange(query(i)) for i in range(40)))
        port = self.server.sockets[0].getsockname()[1]
        self.server.close()
        for writer in list(self.live):
            writer.close()
        await asyncio.wait_for(self.server.wait_closed(), 2)
        await asyncio.sleep(.02)
        self.server = await asyncio.start_server(self.serve, '127.0.0.1', port)
        rs = await asyncio.gather(*(self.pool.exchange(query(i)) for i in range(80)))
        self.assertEqual(len(rs), 80)
        self.assertTrue(all(r[:2] == i.to_bytes(2, 'big') for i, r in enumerate(rs)))
        self.assertEqual(self.pool.slots.qsize(), 4)

    async def test_malformed_queries_do_not_connect(self):
        for q in (b'', b'X'*12, query()[:14]):
            with self.assertRaises(ValueError):
                await self.pool.exchange(q)
        self.assertEqual(self.connections, 0)

if __name__ == '__main__':
    unittest.main()
