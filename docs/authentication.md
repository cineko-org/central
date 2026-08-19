# Authentication boundaries

- Admin routes accept only the `cineko_admin_session` HttpOnly, SameSite=Strict cookie. Client Bearer tokens are not admin credentials.
- Client routes accept only a protocol-versioned Bearer session. Admin cookies are not Client credentials.
- Admin login performs at most four Argon2id verifications concurrently and blocks after 10 failures for 10 minutes by both direct source and normalized user ID.
- Six-digit Client PIN exchange applies the same 10-failure, 10-minute limit to both source and device. A successful exchange resets only its device scope; changing devices cannot reset the source scope.
- Forwarding headers affect source identity and cookie security only when the direct peer is in `CINEKO_TRUSTED_PROXY_CIDRS`. Otherwise Central uses `RemoteAddr` and ignores them.
- Client event streams periodically revalidate the original Bearer session. Expiry, logout, or user deletion closes existing streams.

Central stores only session hashes and Argon2id password hashes. Deployment credentials, tokens, peppers, and the database URL are loaded from `*_FILE` secret paths in production.
