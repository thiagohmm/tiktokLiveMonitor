const { test } = require('node:test');
const assert = require('node:assert/strict');
const { toFollowFlag, resolveIsFollower } = require('./follower');

test('toFollowFlag maps tiktok follow codes', () => {
    const cases = [
        [true, true],
        [false, false],
        [1, true],
        [0, false],
        [2, true],
        ['1', true],
        ['0', false],
        ['2', true],
        ['true', true],
        ['false', false],
        ['', null],
        [null, null],
        [undefined, null]
    ];
    for (const [input, want] of cases) {
        assert.equal(toFollowFlag(input), want, `toFollowFlag(${JSON.stringify(input)})`);
    }
});

test('resolveIsFollower ignores protobuf default isFollower false', () => {
    assert.equal(resolveIsFollower({ isFollower: false }), null);
    assert.equal(resolveIsFollower({ user: { isFollower: false } }, { isFollower: false }), null);
});

test('resolveIsFollower uses identity relative to the host', () => {
    assert.equal(resolveIsFollower({
        userIdentity: { isFollowerOfAnchor: true }
    }), true);
    assert.equal(resolveIsFollower({
        userIdentity: { isFollowerOfAnchor: false }
    }), false);
    assert.equal(resolveIsFollower({
        userIdentity: { isMutualFollowingWithAnchor: true, isFollowerOfAnchor: false }
    }), true);
});

test('resolveIsFollower prefers followStatus over default false', () => {
    assert.equal(resolveIsFollower({
        isFollower: false,
        followInfo: { followStatus: '1' }
    }), true);
    assert.equal(resolveIsFollower({
        isFollower: false,
        followInfo: { followStatus: 0 }
    }), false);
    assert.equal(resolveIsFollower({
        followRole: 1
    }), true);
    assert.equal(resolveIsFollower({
        user: { isFollower: false, followInfo: { followStatus: '2' } }
    }, { isFollower: false, followInfo: { followStatus: '2' } }), true);
});
