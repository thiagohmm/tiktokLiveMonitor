const { WebcastPushConnection } = require('tiktok-live-connector');

// Let's connect to a popular live and observe the gift events
// We'll just exit after a few seconds or a few gifts
const connection = new WebcastPushConnection('tiktok');

connection.on('gift', data => {
    console.log('GIFT:', data.giftName, 'repeatCount:', data.repeatCount, 'repeatEnd:', data.repeatEnd, 'giftType:', data.giftType);
});

connection.connect().then(() => {
    console.log('Connected');
    setTimeout(() => {
        connection.disconnect();
        process.exit(0);
    }, 10000);
}).catch(err => {
    console.error('Connection failed', err);
});
