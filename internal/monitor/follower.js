function toFollowFlag(value) {
    if (value === true || value === 1 || value === '1') return true;
    if (value === false || value === 0 || value === '0') return false;
    if (value === 2 || value === '2') return true; // friends / mutual
    if (typeof value === 'string') {
        const v = value.trim().toLowerCase();
        if (!v) return null;
        if (v === 'true') return true;
        if (v === 'false') return false;
        const n = Number(v);
        if (Number.isFinite(n)) return toFollowFlag(n);
    }
    return null;
}

function identityFollowerFlag(identity) {
    if (!identity || typeof identity !== 'object') return null;
    if (identity.isFollowerOfAnchor === true || identity.isMutualFollowingWithAnchor === true) {
        return true;
    }
    if (identity.isFollowerOfAnchor === false) return false;
    return null;
}

// user.isFollower=false is a protobuf default and does not mean "does not follow".
function resolveIsFollower(...sources) {
    let sawExplicitNonFollower = false;
    for (const src of sources) {
        if (!src || typeof src !== 'object') continue;

        const fromIdentity = identityFollowerFlag(src.userIdentity || src.user_identity);
        if (fromIdentity === true) return true;
        if (fromIdentity === false) sawExplicitNonFollower = true;

        const role = toFollowFlag(src.followRole);
        if (role === true) return true;
        if (role === false) sawExplicitNonFollower = true;

        const followInfo = src.followInfo;
        if (followInfo && typeof followInfo === 'object') {
            const status = toFollowFlag(followInfo.followStatus);
            if (status === true) return true;
            if (status === false) sawExplicitNonFollower = true;
        }

        const followStatus = toFollowFlag(src.followStatus);
        if (followStatus === true) return true;
        if (followStatus === false) sawExplicitNonFollower = true;

        if (src.isFollowerOfAnchor === true) return true;
        if (src.isFollower === true) return true;
    }
    if (sawExplicitNonFollower) return false;
    return null;
}

module.exports = {
    toFollowFlag,
    resolveIsFollower
};
