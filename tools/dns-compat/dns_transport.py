"""Bounded persistent TCP transport for loopback DNS; no answer cache."""
import asyncio


def question_key(wire):
    if len(wire) < 12 or wire[4:6] != b'\x00\x01':
        raise ValueError('single DNS question required')
    pos = 12
    labels = []
    while True:
        if pos >= len(wire):
            raise ValueError('short name')
        size = wire[pos]
        pos += 1
        if not size:
            break
        if size > 63 or pos + size > len(wire):
            raise ValueError('invalid name')
        labels.append(wire[pos:pos + size].lower())
        pos += size
        if pos > 267:
            raise ValueError('name too long')
    if pos + 4 > len(wire):
        raise ValueError('short question')
    return tuple(labels), wire[pos:pos + 4]


class TCPPool:
    """One in-flight question per connection, finite slots and total deadline.

    A connection-level EOF/reset may be retried once because DNS queries are
    read-only. Invalid replies and timeouts are never hidden by retries.
    """
    def __init__(self, host, port, capacity=16, timeout=6):
        self.host, self.port = host, port
        self.timeout = timeout
        self.slots = asyncio.LifoQueue(capacity)
        for _ in range(capacity):
            self.slots.put_nowait(None)

    @staticmethod
    def discard(connection):
        if connection is not None:
            connection[1].close()

    async def exchange(self, query):
        expected = question_key(query)
        if query[2] & 0xf8:
            raise ValueError('standard query required')
        async with asyncio.timeout(self.timeout):
            connection = await self.slots.get()
            reusable = False
            try:
                for attempt in range(2):
                    try:
                        if connection is not None and (connection[0].at_eof() or connection[1].is_closing()):
                            self.discard(connection)
                            connection = None
                        if connection is None:
                            connection = await asyncio.open_connection(self.host, self.port)
                        reader, writer = connection
                        writer.write(len(query).to_bytes(2, 'big') + query)
                        await writer.drain()
                        size = int.from_bytes(await reader.readexactly(2), 'big')
                        if size < 12:
                            raise ValueError('short response')
                        reply = await reader.readexactly(size)
                        if reply[:2] != query[:2] or not reply[2] & 0x80 or question_key(reply) != expected:
                            raise ValueError('mismatched response')
                        reusable = True
                        return reply
                    except (ConnectionError, asyncio.IncompleteReadError):
                        self.discard(connection)
                        connection = None
                        if attempt:
                            raise
            finally:
                if not reusable:
                    self.discard(connection)
                    connection = None
                self.slots.put_nowait(connection)

    async def close(self):
        while not self.slots.empty():
            self.discard(self.slots.get_nowait())
