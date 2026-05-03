#!/usr/bin/env python3
import argparse
import asyncio
import inspect
import json
import os
import sys


def write_result(payload, exit_code=0):
    print(json.dumps(payload, ensure_ascii=False))
    return exit_code


def parser():
    p = argparse.ArgumentParser(description="Envia uma mensagem para um chat TikTok LIVE usando TikTokLive.")
    p.add_argument("--username", required=True)
    p.add_argument("--message", required=True)
    p.add_argument("--session-id", required=True)
    p.add_argument("--tt-target-idc", default=os.environ.get("TIKTOK_TT_TARGET_IDC", ""))
    p.add_argument("--room-id", default="")
    return p


async def maybe_await(value):
    if inspect.isawaitable(value):
        return await value
    return value


async def start_client(client, room_id):
    kwargs = {}
    if room_id:
        kwargs["room_id"] = room_id

    try:
        return await client.start(**kwargs)
    except TypeError:
        if room_id:
            return await client.start(room_id=room_id)
        return await client.start()


async def disconnect_client(client):
    for name in ("disconnect", "stop"):
        method = getattr(client, name, None)
        if not method:
            continue
        try:
            await maybe_await(method())
        except TypeError:
            await maybe_await(method(close_client=True))
        return


async def send_chat(args):
    try:
        from TikTokLive import TikTokLiveClient
    except ImportError as exc:
        raise RuntimeError("TikTokLive não está instalado. Rode: pip install -r requirements-python.txt") from exc

    username = args.username.strip().lstrip("@")
    client = TikTokLiveClient(unique_id=f"@{username}")

    web = getattr(client, "web", None)
    if web and hasattr(web, "set_session"):
        web.set_session(args.session_id, args.tt_target_idc or None)

    task = None
    try:
        task = await start_client(client, args.room_id or None)
        await asyncio.sleep(0.5)

        if hasattr(client, "send_room_chat"):
            response = await client.send_room_chat(args.message)
        elif hasattr(client, "send_message"):
            response = await client.send_message(args.message, session_id=args.session_id)
        elif web and hasattr(web, "send_room_chat"):
            response = await web.send_room_chat(
                content=args.message,
                room_id=args.room_id or getattr(client, "room_id", None),
                session_id=args.session_id,
                tt_target_idc=args.tt_target_idc or None,
            )
        else:
            raise RuntimeError("Esta versão da TikTokLive não expõe send_room_chat/send_message.")

        # Verificar se a resposta indica sucesso
        # O TikTok costuma retornar algo como {"status_code": 0} para sucesso
        status_code = -1
        status_msg = ""
        
        if isinstance(response, dict):
            status_code = response.get("status_code", -1)
            status_msg = response.get("status_msg", "")
        elif hasattr(response, "status_code"):
            status_code = response.status_code
        elif hasattr(response, "status"):
            status_code = response.status
        elif hasattr(response, "get"):
             status_code = response.get("status_code", -1)

        if status_code != 0:
            error_msg = f"TikTok retornou erro (status_code={status_code})"
            if status_msg:
                error_msg += f": {status_msg}"
            
            # Tentar extrair mais detalhes se for um objeto complexo
            resp_info = str(response)
            try:
                if hasattr(response, "__dict__"):
                    resp_info = str(response.__dict__)
            except:
                pass

            return {
                "ok": False,
                "error": error_msg,
                "response_raw": resp_info,
                "response_type": str(type(response))
            }

        return {"ok": True, "response": response if isinstance(response, (dict, list, str, int, float, bool)) else str(response)}
    finally:
        try:
            await disconnect_client(client)
        finally:
            if task and hasattr(task, "cancel") and not task.done():
                task.cancel()


def main():
    args = parser().parse_args()

    # sessionId é obrigatório para o sender funcionar
    if not args.session_id:
        return write_result({
            "ok": False,
            "error": "Cookie sessionid não encontrado. Faça login na janela do bot e tente novamente."
        }, 2)

    try:
        result = asyncio.run(send_chat(args))
        return write_result(result, 0)
    except Exception as exc:
        return write_result({"ok": False, "error": str(exc)}, 1)


if __name__ == "__main__":
    sys.exit(main())
