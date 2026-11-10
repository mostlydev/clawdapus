# Task-home gate (ADR-027): a fixed-destination TCP forwarder. It accepts
# connections on one port and relays each to one upstream given on the command
# line. It is not a proxy: it parses nothing and has no other destination.
import asyncio, sys

host, port, listen = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])


async def pipe(reader, writer):
    try:
        while True:
            data = await reader.read(65536)
            if not data:
                break
            writer.write(data)
            await writer.drain()
    except Exception:
        pass
    finally:
        try:
            writer.close()
        except Exception:
            pass


async def handle(reader, writer):
    try:
        up_reader, up_writer = await asyncio.open_connection(host, port)
    except Exception:
        writer.close()
        return
    await asyncio.gather(pipe(reader, up_writer), pipe(up_reader, writer))


async def main():
    server = await asyncio.start_server(handle, "0.0.0.0", listen)
    async with server:
        await server.serve_forever()


asyncio.run(main())
