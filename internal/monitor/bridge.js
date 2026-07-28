const { WebcastPushConnection } = require('tiktok-live-connector');

let connection = null;
let currentUsername = '';

function send(type, data) {
    const msg = JSON.stringify({ type, data }) + '\n';
    process.stdout.write(msg);
}

function getUser(data) {
    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    return {
        uniqueId: data.uniqueId || user.uniqueId || null,
        nickname: data.nickname || user.nickname || data.uniqueId || user.uniqueId || null,
        isFollower: typeof data.isFollower === 'boolean' ? data.isFollower :
                    typeof user.isFollower === 'boolean' ? user.isFollower : null
    };
}

process.stdin.on('data', async (chunk) => {
    const lines = chunk.toString().trim().split('\n');
    for (const line of lines) {
        try {
            const cmd = JSON.parse(line);
            await handleCommand(cmd);
        } catch (e) {
            send('error', { message: e.message });
        }
    }
});

async function handleCommand(cmd) {
    switch (cmd.action) {
        case 'connect':
            await doConnect(cmd.username);
            break;
        case 'disconnect':
            doDisconnect();
            break;
        case 'get-state':
            send('state', { connected: !!connection, username: currentUsername });
            break;
    }
}

async function doConnect(username) {
    if (connection) {
        doDisconnect();
    }

    currentUsername = username;
    connection = new WebcastPushConnection(username, {
        processInitialData: false,
        fetchRoomInfoOnConnect: true,
        enableExtendedGiftInfo: false
    });

    connection.on('connected', () => {
        send('connection-status', { success: true, username });
    });

    connection.on('disconnected', () => {
        send('connection-status', { success: false, error: 'Desconectado' });
        connection = null;
    });

    connection.on('chat', (data) => {
        const user = getUser(data);
        send('new-chat-message', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            comment: data.comment || '',
            timestamp: Date.now(),
            isFollower: user.isFollower
        });
    });

    connection.on('gift', (data) => {
        const user = getUser(data);
        const giftType = data.giftDetails?.giftType ?? data.giftType ?? 0;
        const repeatEnd = data.repeatEnd ?? true;
        const repeatCount = data.repeatCount ?? 1;
        const giftName = data.giftDetails?.giftName || data.giftName || '';

        const payload = {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            giftName,
            repeatCount,
            repeatEnd,
            giftType,
            isFollower: user.isFollower,
            timestamp: Date.now()
        };

        send('any-gift-received', payload);

        const isTarget = giftType === 1 ? repeatEnd : true;
        if (isTarget) {
            send('new-gift-user', payload);
        }
    });

    connection.on('member', (data) => {
        const user = getUser(data);
        send('live-user-connected', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            isFollower: user.isFollower
        });
    });

    connection.on('follow', (data) => {
        const user = getUser(data);
        send('new-follower', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            isFollower: true
        });
    });

    connection.on('share', (data) => {
        const user = getUser(data);
        send('new-social-event', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            isFollower: user.isFollower
        });
    });

    connection.on('roomPin', (data) => {
        const user = getUser(data);
        const pinId = data.pinId || data.messageId || Date.now().toString();
        send('pinned-comment', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            comment: data.comment || data.content || '[fixado]',
            pinId: String(pinId),
            timestamp: Date.now(),
            isFollower: user.isFollower
        });
    });

    connection.on('error', (err) => {
        send('error', { message: err.message || String(err) });
    });

    try {
        await connection.connect();
    } catch (err) {
        send('connection-status', {
            success: false,
            error: `Falha ao conectar: ${err.message}`
        });
        connection = null;
    }
}

function doDisconnect() {
    if (connection) {
        try { connection.disconnect(); } catch (e) {}
        connection = null;
    }
    currentUsername = '';
}

process.on('uncaughtException', (err) => {
    send('error', { message: err.message });
});

process.on('unhandledRejection', (err) => {
    send('error', { message: err.message || String(err) });
});
