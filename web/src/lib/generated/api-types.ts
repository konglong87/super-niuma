/* eslint-disable */
// This file is generated from docs/swagger.json. Run `npm --prefix web run generate:api-types`.

export interface paths {
    "/agent/slash-commands": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List Web Agent slash command suggestions */
        get: {
            parameters: {
                query?: {
                    /** @description Workspace cwd */
                    cwd?: string;
                    /** @description Limit */
                    limit?: number;
                    /** @description Slash command prefix */
                    prefix?: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListAgentSlashCommandsResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/agent/workspaces/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Validate a local Web Agent workspace path */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Workspace cwd */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerAgentWorkspaceValidateRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentWorkspaceValidateResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/health": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Health check */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerHealthResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/livez": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Liveness probe
         * @description 进程存活探针。不鉴权、不探任何依赖，只要 HTTP server 还在响应就返回 200，供 k8s livenessProbe 与 LB 使用。
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerLivenessResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/local/skills": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List locally installed skills
         * @description Lists skills discovered from the current workspace, user skill roots, bundled skills, MCP roots, and installed plugins. These records are read-only local state and are not tenant skill records.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListLocalSkillsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                    };
                };
                /** @description Method not allowed */
                405: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                    };
                };
                /** @description Local discovery failed */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/metrics": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Export Prometheus telemetry metrics */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description Prometheus text format */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/plain": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/plain": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/attachments/presign": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Create a mobile attachment upload contract */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Attachment upload metadata */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerMobileAttachmentPresignRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerMobileAttachmentPresignResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List mobile chat sessions */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListSessionsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Create mobile chat session */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerMobileSessionRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/sessions/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get mobile chat session */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerMobileSessionDetailResponse"];
                    };
                };
            };
        };
        /** Replace mobile chat session fields */
        put: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerMobileSessionRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        post?: never;
        /** Archive mobile chat session */
        delete: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerArchiveResponse"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        /** Update mobile chat session */
        patch: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerMobileSessionRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/mobile/chat/sessions/{id}/branch": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Branch a mobile chat session */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Source session ID */
                    id: number;
                };
                cookie?: never;
            };
            /** @description Branch request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerMobileBranchRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerMobileBranchResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/sessions/{id}/messages": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List mobile chat messages */
        get: {
            parameters: {
                query?: {
                    /** @description Return messages after this turn */
                    after_turn?: number;
                    /** @description Pagination cursor */
                    cursor?: string;
                    /** @description Limit */
                    limit?: number;
                };
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerMobileMessageListResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/sessions/{id}/messages/{message_id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cancel a mobile assistant message */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                    /** @description Message ID */
                    message_id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerMobileCancelResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/sessions/{id}/messages/{message_id}/regenerate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Regenerate a mobile assistant message */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                    /** @description Assistant message ID */
                    message_id: number;
                };
                cookie?: never;
            };
            /** @description Regenerate request */
            requestBody?: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerMobileRegenerateRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSSEEvent"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/sessions/{id}/messages/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Send a mobile chat message and stream assistant response */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            /** @description Message stream request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerMobileMessageStreamRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSSEEvent"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/mobile/chat/ws": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Open mobile chat WebSocket sync, Goal event, and control channel
         * @description Sends connected first. Clients can send subscribe with session_id for message_event fanout, subscribe_goal with goal_id for goal_event fanout, ping for pong, and cancel with session/message identifiers for active stream cancellation. Message and Goal fanout are scoped to the mobile JWT tenant/user.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description Switching Protocols */
                101: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/prompt-dump": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Open the built-in prompt dump viewer WebUI
         * @description Serves a local WebUI for inspecting prompt dump JSONL records, filtering by session_id, and opening full raw request details when the dump was captured with NIUMA_DUMP_PROMPT_FULL=true.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description HTML prompt dump viewer */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/prompt-dump/api/records": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List prompt dump records
         * @description Reads the local prompt dump JSONL file from NIUMA_DUMP_PROMPT_JSON or /tmp/niuma-tui-prompt.jsonl. include_request=true returns full raw model requests and may expose sensitive prompt context.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Include full raw request payload */
                    include_request?: boolean;
                    /** @description Maximum records to return, capped at 1000 */
                    limit?: number;
                    /** @description Filter by TUI session ID */
                    session_id?: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPromptDumpRecordsResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/query": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Run a prompt through the local query loop */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Query request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.QueryRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerQueryResult"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/readyz": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Readiness probe
         * @description 就绪探针。不鉴权，逐个探活已配置的依赖（MySQL、配额 Redis、mobile 用量 Redis），任一不可用返回 503。响应只含 ok/unavailable，不泄漏错误详情。
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerReadinessResponse"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerReadinessResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/background": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List local background jobs and loops */
        get: {
            parameters: {
                query?: {
                    /** @description Filter by background job kind, e.g. loop */
                    kind?: string;
                    /** @description Maximum jobs to return */
                    limit?: number;
                    /** @description Log tail bytes to include */
                    tail?: number;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerRuntimeBackgroundListResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Create a local loop */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerRuntimeLoopRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.RuntimeBackgroundJob"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/background/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Update a local loop */
        patch: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Background job id or schedule id */
                    id: string;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerRuntimeLoopRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.RuntimeBackgroundJob"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/runtime/background/{id}/logs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read local background job logs */
        get: {
            parameters: {
                query?: {
                    /** @description Log tail bytes */
                    tail?: number;
                };
                header?: never;
                path: {
                    /** @description Background job id */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerRuntimeBackgroundLogsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/background/{id}/run": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Run a local loop once now */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Background job id or schedule id */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.RuntimeBackgroundJob"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/background/{id}/runs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List local loop run history */
        get: {
            parameters: {
                query?: {
                    /** @description Maximum run records */
                    limit?: number;
                };
                header?: never;
                path: {
                    /** @description Background job id or schedule id */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerRuntimeBackgroundRunsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/background/{id}/stop": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Stop a local background job or loop */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Background job id or schedule id */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerRuntimeBackgroundStopResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/background/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read local scheduler events */
        get: {
            parameters: {
                query?: {
                    /** @description Maximum event records */
                    limit?: number;
                    /** @description Byte offset returned by previous response */
                    offset?: number;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerRuntimeBackgroundEventsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Read the global settings.json document
         * @description Returns the raw global settings document with secret values masked. `masked` lists the dotted paths whose values were replaced.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.GlobalSettingsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /**
         * Replace the global settings.json document
         * @description Accepts a full settings object. Unknown keys are preserved, but a type mismatch on a known field is rejected before the file is touched. POST behaves identically to PUT.
         */
        put: {
            parameters: {
                query?: never;
                header?: {
                    /** @description Revision from GET (raw or quoted ETag); stale revisions return 409 */
                    "If-Match"?: string;
                    /** @description Fallback provider index used to restore promoted credentials during validation and save */
                    "X-Settings-Promoted-Provider-Index"?: number;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.GlobalSettingsSaveResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/settings/effective": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Inspect global file settings and safe server startup configuration
         * @description File settings use only the global settings.json (NIUMA_CONFIG_DIR relocation supported), without workspace or legacy discovery. File sources exclude process environment, CLI, profile and run overrides. Startup or status callback values do not represent active run configuration.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.EffectiveSettingsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/settings/environments": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List authorized settings environments and shared global file scope */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Source tenant key */
                    "X-Tenant-Key": string;
                    /** @description Source operator user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SettingsEnvironmentsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/settings/environments/{environment}/{resource}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Use existing Profile management APIs in a preconfigured environment
         * @description Source identity must match the server allowlist and be owner/admin. Target tenant/user and database are fixed by server configuration. Resource permits tenant/agent-profiles and its existing lifecycle subpaths, tenant/agent-profile-assignment, and GET-only tenant/channel-accounts and tenant/messages. Global settings remain on the original shared runtime/settings endpoint. No chat execution or worker lifecycle routes are exposed. Responses and request bodies retain each delegated API contract.
         */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Source tenant key */
                    "X-Tenant-Key": string;
                    /** @description Source operator user key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Preconfigured environment id */
                    environment: string;
                    /** @description Existing settings resource path, for example tenant/agent-profiles */
                    resource: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request5"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": Record<string, never>;
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /**
         * Use existing Profile management APIs in a preconfigured environment
         * @description Source identity must match the server allowlist and be owner/admin. Target tenant/user and database are fixed by server configuration. Resource permits tenant/agent-profiles and its existing lifecycle subpaths, tenant/agent-profile-assignment, and GET-only tenant/channel-accounts and tenant/messages. Global settings remain on the original shared runtime/settings endpoint. No chat execution or worker lifecycle routes are exposed. Responses and request bodies retain each delegated API contract.
         */
        put: {
            parameters: {
                query?: never;
                header: {
                    /** @description Source tenant key */
                    "X-Tenant-Key": string;
                    /** @description Source operator user key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Preconfigured environment id */
                    environment: string;
                    /** @description Existing settings resource path, for example tenant/agent-profiles */
                    resource: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request5"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": Record<string, never>;
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /**
         * Use existing Profile management APIs in a preconfigured environment
         * @description Source identity must match the server allowlist and be owner/admin. Target tenant/user and database are fixed by server configuration. Resource permits tenant/agent-profiles and its existing lifecycle subpaths, tenant/agent-profile-assignment, and GET-only tenant/channel-accounts and tenant/messages. Global settings remain on the original shared runtime/settings endpoint. No chat execution or worker lifecycle routes are exposed. Responses and request bodies retain each delegated API contract.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Source tenant key */
                    "X-Tenant-Key": string;
                    /** @description Source operator user key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Preconfigured environment id */
                    environment: string;
                    /** @description Existing settings resource path, for example tenant/agent-profiles */
                    resource: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request5"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": Record<string, never>;
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /**
         * Use existing Profile management APIs in a preconfigured environment
         * @description Source identity must match the server allowlist and be owner/admin. Target tenant/user and database are fixed by server configuration. Resource permits tenant/agent-profiles and its existing lifecycle subpaths, tenant/agent-profile-assignment, and GET-only tenant/channel-accounts and tenant/messages. Global settings remain on the original shared runtime/settings endpoint. No chat execution or worker lifecycle routes are exposed. Responses and request bodies retain each delegated API contract.
         */
        delete: {
            parameters: {
                query?: never;
                header: {
                    /** @description Source tenant key */
                    "X-Tenant-Key": string;
                    /** @description Source operator user key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Preconfigured environment id */
                    environment: string;
                    /** @description Existing settings resource path, for example tenant/agent-profiles */
                    resource: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request5"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": Record<string, never>;
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        /**
         * Use existing Profile management APIs in a preconfigured environment
         * @description Source identity must match the server allowlist and be owner/admin. Target tenant/user and database are fixed by server configuration. Resource permits tenant/agent-profiles and its existing lifecycle subpaths, tenant/agent-profile-assignment, and GET-only tenant/channel-accounts and tenant/messages. Global settings remain on the original shared runtime/settings endpoint. No chat execution or worker lifecycle routes are exposed. Responses and request bodies retain each delegated API contract.
         */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Source tenant key */
                    "X-Tenant-Key": string;
                    /** @description Source operator user key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Preconfigured environment id */
                    environment: string;
                    /** @description Existing settings resource path, for example tenant/agent-profiles */
                    resource: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request5"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": Record<string, never>;
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/runtime/settings/promote-provider": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Promote a fallback provider into the primary settings route
         * @description Returns a masked draft with the selected fallback provider copied to the top-level route. The global settings file is not changed until PUT /runtime/settings.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Current settings draft and fallback provider index */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.GlobalSettingsPromoteRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.GlobalSettingsPromoteResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/settings/test-provider": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Test provider models catalog connectivity
         * @description Restores masked credentials and probes the provider models catalog with a ten second timeout. Never saves settings, follows redirects, or performs inference. A catalog failure does not prove inference is unavailable.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Settings document and optional named provider */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SettingsProviderTestRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SettingsProviderTestResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/runtime/settings/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Validate a global settings document without saving or network requests */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SettingsValidationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Local session snapshot */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSnapshotResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Runtime status snapshot */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSnapshotResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profile-assignment": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get or assign profile surface */
        get: {
            parameters: {
                query: {
                    /** @description Runtime surface */
                    surface: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request2"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfileAssignment"];
                    };
                };
            };
        };
        /** Get or assign profile surface */
        put: {
            parameters: {
                query: {
                    /** @description Runtime surface */
                    surface: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request2"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfileAssignment"];
                    };
                };
            };
        };
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List agent profiles */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                    /** @description Profile status */
                    status?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        put?: never;
        /** Save agent profile draft */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request4"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfile"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get agent profile */
        get: {
            parameters: {
                query?: {
                    /** @description Profile version */
                    version?: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfile"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Save agent profile draft */
        patch: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request4"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfile"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/archive": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Archive agent profile */
        post: {
            parameters: {
                query: {
                    /** @description Profile version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/bot-binding": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage agent profile bot binding */
        get: {
            parameters: {
                query: {
                    /** @description Profile version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request3"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfileChannelBinding"];
                    };
                };
            };
        };
        /** Manage agent profile bot binding */
        put: {
            parameters: {
                query: {
                    /** @description Profile version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request3"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfileChannelBinding"];
                    };
                };
            };
        };
        post?: never;
        /** Manage agent profile bot binding */
        delete: {
            parameters: {
                query: {
                    /** @description Profile version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["Request3"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfileChannelBinding"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/conversations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List conversations for an Agent Profile
         * @description Returns safe DM/group conversation summaries and Team links for a tenant-visible profile.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Maximum number of conversations */
                    limit?: number;
                    /** @description Profile version */
                    version?: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfileConversationCatalog"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/effective": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Preview effective agent profile */
        get: {
            parameters: {
                query?: {
                    /** @description Requested effort override */
                    effort?: string;
                    /** @description Requested language override */
                    language?: string;
                    /** @description Requested max tokens */
                    max_tokens?: number;
                    /** @description Requested max turns */
                    max_turns?: number;
                    /** @description Requested output style override */
                    output_style?: string;
                    /** @description Runtime surface */
                    surface?: string;
                    /** @description Profile version */
                    version?: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerEffectiveAgentProfile"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/publish": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Publish agent profile */
        post: {
            parameters: {
                query: {
                    /** @description Profile version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/rollback": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Roll back agent profile */
        post: {
            parameters: {
                query: {
                    /** @description Profile version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentProfile"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-profiles/{key}/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Validate agent profile */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Profile key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request4"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-provisionings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage profile agent provisioning sessions */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        put?: never;
        /** Manage profile agent provisioning sessions */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-provisionings/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage profile agent provisioning sessions */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-provisionings/{id}/{action}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Manage profile agent provisioning sessions */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-provisionings/{id}/logs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage profile agent provisioning sessions */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-provisionings/{id}/preflight": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Manage profile agent provisioning sessions */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-provisionings/overview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage profile agent provisioning sessions */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant sub-agent tasks */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListAgentTasksResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Create tenant sub-agent task metadata */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Agent task metadata */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerCreateAgentTaskRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerCreateAgentTaskResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant sub-agent task */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTaskResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Update tenant sub-agent task status or metadata */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            /** @description Agent task update */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerUpdateAgentTaskRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerUpdateAgentTaskResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cancel a running tenant sub-agent task */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerCancelAgentTaskResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant sub-agent task events */
        get: {
            parameters: {
                query?: {
                    /** @description Return events with id greater than this cursor */
                    after_id?: number;
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListAgentTaskEventsResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/events/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream tenant sub-agent task events over SSE
         * @description Server-Sent Events stream. Emits a `connected` event, then one event per task event as it lands (polled every 250ms), and a terminal event when the task finishes. Use `after_id` to resume without replaying.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Resume after this event id */
                    after_id?: number;
                    /** @description Maximum events per poll (default 200) */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description SSE stream of agent task events */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": string;
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/message": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Send a message and start the tenant sub-agent task runner */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            /** @description Agent task message; content may be empty when image attachments are provided */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerAgentTaskMessageRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTaskMessageResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/pending-input-settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get or update pending input queue settings */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputSettingsRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: boolean;
                        };
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Get or update pending input queue settings */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputSettingsRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: boolean;
                        };
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/pending-inputs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Manage pending user inputs for a tenant agent task
         * @description Enter while a task is running queues a confirmed input. Move-up only changes order; it never starts a runner.
         */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Accepted */
                202: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Too Many Requests */
                429: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /**
         * Manage pending user inputs for a tenant agent task
         * @description Enter while a task is running queues a confirmed input. Move-up only changes order; it never starts a runner.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Accepted */
                202: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Too Many Requests */
                429: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/pending-inputs/{input_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Manage pending user inputs for a tenant agent task
         * @description Enter while a task is running queues a confirmed input. Move-up only changes order; it never starts a runner.
         */
        delete: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                    /** @description Pending input ID */
                    input_id: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Accepted */
                202: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Too Many Requests */
                429: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        /**
         * Manage pending user inputs for a tenant agent task
         * @description Enter while a task is running queues a confirmed input. Move-up only changes order; it never starts a runner.
         */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                    /** @description Pending input ID */
                    input_id: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Accepted */
                202: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Too Many Requests */
                429: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/pending-inputs/{input_id}/{action}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Manage pending user inputs for a tenant agent task
         * @description Enter while a task is running queues a confirmed input. Move-up only changes order; it never starts a runner.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Pending input action */
                    action: string;
                    /** @description Agent task ID */
                    id: number;
                    /** @description Pending input ID */
                    input_id: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerPendingInputRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Accepted */
                202: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPendingInputResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Too Many Requests */
                429: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/permissions/{request_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Resolve a pending Web Agent permission request */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                    /** @description Permission request ID */
                    request_id: string;
                };
                cookie?: never;
            };
            /** @description Permission decision */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerAgentTaskPermissionRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTaskPermissionResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-tasks/{id}/questions/{request_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Answer a pending question and continue the original tenant agent task
         * @description Persists the answer before waking the live Run. Identical answer retries are idempotent; changed, cancelled or expired answers return 409.
         */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Agent task ID */
                    id: number;
                    /** @description Question request ID */
                    request_id: string;
                };
                cookie?: never;
            };
            /** @description Non-empty answer, at most 16384 bytes */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerAgentTaskQuestionAnswerRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTaskQuestionAnswerResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-teams": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List agent teams */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                    /** @description Team status */
                    status?: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        put?: never;
        /** Save agent team draft */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request6"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTeam"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get agent team */
        get: {
            parameters: {
                query?: {
                    /** @description Team version */
                    version?: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTeam"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Save agent team draft */
        patch: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["Request6"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTeam"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/agent-teams/{key}/archive": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Publish or archive agent team */
        post: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/bindings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage agent team channel bindings */
        get: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerAgentTeamBindingArray"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        /** Manage agent team channel bindings */
        put: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerAgentTeamBindingArray"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/members": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Manage agent team members */
        get: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerAgentTeamMemberArray"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        /** Manage agent team members */
        put: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerAgentTeamMemberArray"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/publish": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Publish or archive agent team */
        post: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/rollback": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Roll back agent team */
        post: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTeam"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/runs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List agent team runs */
        get: {
            parameters: {
                query: {
                    /** @description Limit */
                    limit?: number;
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/runs/{run_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get or cancel agent team run */
        get: {
            parameters: {
                query: {
                    /** @description Pinned team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                    /** @description Team run id */
                    run_id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerAgentTeamRun"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/runs/{run_id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cancel agent team run */
        post: {
            parameters: {
                query: {
                    /** @description Pinned team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                    /** @description Team run id */
                    run_id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/runs/{run_id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get member events and mailbox timeline for an agent team run */
        get: {
            parameters: {
                query: {
                    /** @description Limit */
                    limit?: number;
                    /** @description Pinned team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                    /** @description Team run id */
                    run_id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-teams/{key}/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Validate agent team */
        post: {
            parameters: {
                query: {
                    /** @description Team version */
                    version: number;
                };
                header?: never;
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            /** @description Team graph */
            requestBody: {
                content: {
                    "application/json": {
                        [key: string]: unknown;
                    };
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/agent-types": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List extensible Agent role and execution kind metadata
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowTypes"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/audit": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant audit logs */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search action, resource type, resource id, or trace id */
                    search?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListAuditLogsResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/automem/candidates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List pending AutoMem candidates */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListMemoriesResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/automem/review": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Approve or reject an AutoMem candidate */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description AutoMem review request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerAutoMemoryReviewRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/channel-accounts": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List safe channel account metadata */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": {
                            [key: string]: unknown;
                        };
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/context": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Resolve current tenant context */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantContext"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/documents": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant documents */
        get: {
            parameters: {
                query?: {
                    /** @description Return document history */
                    history?: boolean;
                    /** @description Limit */
                    limit?: number;
                    /** @description Document type. When set and history=false, returns the active document. */
                    type?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListDocumentsResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Save tenant document */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Document request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantDocumentRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/effective-skills": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List effective tenant skills */
        get: {
            parameters: {
                query?: {
                    /** @description Only enabled skills */
                    enabled?: boolean;
                    /** @description Limit */
                    limit?: number;
                    /** @description Skill key. When present, returns one effective skill instead of list. */
                    skill_key?: string;
                    /** @description Skill version for detail lookup */
                    version?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListEffectiveSkillsResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/feishu/onboarding": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create a Feishu bot through device authorization
         * @description Starts the shared Feishu onboarding flow, exposes a redacted QR session, and persists the protected credentials after approval.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/feishu/onboarding/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Create a Feishu bot through device authorization
         * @description Starts the shared Feishu onboarding flow, exposes a redacted QR session, and persists the protected credentials after approval.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/feishu/onboarding/{id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create a Feishu bot through device authorization
         * @description Starts the shared Feishu onboarding flow, exposes a redacted QR session, and persists the protected credentials after approval.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/feishu/onboarding/cli": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Create a Feishu bot through device authorization
         * @description Starts the shared Feishu onboarding flow, exposes a redacted QR session, and persists the protected credentials after approval.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/feishu/onboarding/cli/install": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create a Feishu bot through device authorization
         * @description Starts the shared Feishu onboarding flow, exposes a redacted QR session, and persists the protected credentials after approval.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: never;
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant goals */
        get: {
            parameters: {
                query?: {
                    /** @description Only active goals */
                    active?: boolean;
                    /** @description Limit */
                    limit?: number;
                    /** @description Goal status */
                    status?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListGoalsResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Create tenant goal */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Goal metadata */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerCreateGoalRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerGoalResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant goal */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerGoalResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Update tenant goal */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            /** @description Goal update */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerUpdateGoalRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerGoalResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/goals/{id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant goal events */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListGoalEventsResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals/{id}/evidence": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant goal evidence */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListGoalEvidenceResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals/{id}/plan": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant goal plan */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerGoalPlanResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals/{id}/resume": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Resume tenant goal */
        post: {
            parameters: {
                query?: {
                    /** @description Allow failed goals to resume */
                    force?: boolean;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerGoalResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals/{id}/run": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Run one tenant goal turn
         * @description Runs one Goal turn through the server query runner by default. continuous=true runs a bounded synchronous loop up to max_turns while reusing Goal locks, budgets, stop control, audit, and events; detached server background execution is intentionally not implied. Defaults to deterministic evaluation; evaluator=model enables model-classified evaluation with deterministic fallback for ambiguous results. When the Goal has a valid local session_id, the API runner creates the same per-turn local transcript checkpoint used by the CLI runner before executing each turn.
         */
        post: {
            parameters: {
                query?: {
                    /** @description Run a bounded synchronous loop instead of one turn */
                    continuous?: boolean;
                    /** @description Goal evaluator: deterministic or model */
                    evaluator?: string;
                    /** @description Maximum turns for continuous=true, default 5, max 20 */
                    max_turns?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerRunGoalResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/goals/{id}/stop": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Stop tenant goal */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Goal ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerGoalResponse"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/images/capabilities": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List configured tenant image model capabilities */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerImageCapabilitiesResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/knowledge/documents": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant knowledge documents */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListKnowledgeDocumentsResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Save tenant knowledge document */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Knowledge document request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantKnowledgeDocumentRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/knowledge/search": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Search tenant knowledge chunks */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Knowledge search request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantKnowledgeSearchRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerKnowledgeSearchResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/managed-memory": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant managed memory */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListMemoriesResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Save tenant managed memory */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Memory request. Category is forced to managed. */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantMemoryRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/media/assets/{asset_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read a tenant image asset */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Asset ID */
                    asset_id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "image/png": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "image/png": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "image/png": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "image/png": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/memories": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant memories */
        get: {
            parameters: {
                query?: {
                    /** @description Memory category */
                    category?: string;
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListMemoriesResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Upsert tenant memory */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Memory request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantMemoryRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/memory-review/candidates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List pending memory review candidates
         * @description Lists explicit remember and AutoMem pending candidates. Pending candidates are not injected into prompt context until approved.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Candidate type: explicit_remember or automem_candidate */
                    candidate_type?: string;
                    /** @description Limit */
                    limit?: number;
                    /** @description Risk status, for example needs_review or low_risk */
                    risk_status?: string;
                    /** @description Source session ID */
                    source_session_id?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListMemoriesResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/memory-review/review": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve, reject, or archive a memory review candidate
         * @description Approving creates an active user memory. Reject/archive keep the candidate out of prompt context.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Memory review request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerMemoryReviewRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/messages": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant session messages */
        get: {
            parameters: {
                query: {
                    /** @description Limit */
                    limit?: number;
                    /** @description Session ID */
                    session_id: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListMessagesResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Upsert tenant session message */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Message request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantMessageRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/profile": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant profile */
        get: {
            parameters: {
                query?: {
                    /** @description Profile version */
                    version?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantProfile"];
                    };
                };
            };
        };
        put?: never;
        /** Save tenant profile */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Profile request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantProfileRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/prompt-templates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List prompt templates
         * @description Success returns JSON; errors return plain text.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Exact category */
                    category?: string;
                    /** @description Maximum items (1-200, default 100) */
                    limit?: number;
                    /** @description Search title, content or category */
                    search?: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPromptTemplate"][];
                        "text/plain": components["schemas"]["internal_server.SwaggerPromptTemplate"][];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
            };
        };
        put?: never;
        /**
         * Create or update a prompt template by owner and title
         * @description Success returns JSON; errors return plain text.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Template; omit ID to upsert by owner/title */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerPromptTemplateRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPromptTemplate"];
                        "text/plain": components["schemas"]["internal_server.SwaggerPromptTemplate"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Update an owned prompt template
         * @description Success returns JSON; errors return plain text.
         */
        patch: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Template with a nonzero ID */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerPromptTemplateRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerPromptTemplate"];
                        "text/plain": components["schemas"]["internal_server.SwaggerPromptTemplate"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": string;
                        "text/plain": string;
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/prompt-templates/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Delete an owned prompt template
         * @description Success has no body; errors return plain text.
         */
        delete: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Prompt template ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description No Content */
                204: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content?: never;
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/plain": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/plain": string;
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/plain": string;
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/plain": string;
                    };
                };
            };
        };
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/quota/config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant quota config */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantQuotaConfig"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /** Create or update tenant quota config */
        put: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Owner or admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Tenant quota config */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantQuotaConfigRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantQuotaConfig"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/quota/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant quota events */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search event type, limit type, source, route, model, or trace id */
                    search?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListTenantQuotaEventsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/conversations/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Subscribe to managed conversations
         * @description One SSE connection carries conversation pages for up to 32 authorized session refs. Each has an independent cursor; disconnect never cancels runs. Reconnect with per-session cursors.
         */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            /** @description Session refs and cursors */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSessionConversationSubscribe"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.sessionConversationPage"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List Session Control sessions
         * @description 返回当前认证 tenant/user 的有界会话状态。source=local 仅支持只读；响应不包含 transcript 或消息正文。
         */
        get: {
            parameters: {
                query: {
                    /** @description Maximum sessions */
                    limit?: number;
                    /** @description Session namespace */
                    source: "tenant" | "local";
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlSessionListResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        put?: never;
        /**
         * Create a managed Session Control session
         * @description 创建 tenant session。身份只来自认证上下文；body 不能设置 tenant、user、actor 或 trace。相同 Idempotency-Key 与相同语义返回相同 readback；不同语义返回 idempotency_conflict。
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Mutation idempotency key, at most 128 bytes */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Managed session request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSessionControlCreateRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlOperationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Read a Session Control snapshot
         * @description 返回 namespaced ref 对应的有界状态快照。Local session 是只读的；响应不包含 transcript、消息正文或私有 locator。
         */
        get: {
            parameters: {
                query?: {
                    /** @description Include safe relation metadata */
                    include_links?: boolean;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Namespace-local session key */
                    id: string;
                    /** @description Session namespace */
                    source: "tenant" | "local";
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlSessionResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/attachments": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Attach bounded session handoff sources
         * @description 通过 Session Control Attach 将 namespaced source 的有界 handoff 包绑定到显式 target task。不会复制完整 transcript。Local target 写入始终返回 forbidden。
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Mutation idempotency key, at most 128 bytes */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Managed target session key */
                    id: string;
                    /** @description Must be tenant */
                    source: "tenant";
                };
                cookie?: never;
            };
            /** @description Handoff attachment request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSessionControlAttachRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlOperationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unprocessable Entity */
                422: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/compact": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Compact a managed session transcript
         * @description 将当前 managed session 的可读对话压缩为一个持久化 compact_summary 事件。Local session 写入始终返回 forbidden；事件写入遵循当前会话事件后端，不执行双写。
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Mutation idempotency key, at most 128 bytes */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Managed session key */
                    id: string;
                    /** @description Must be tenant */
                    source: "tenant";
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerSessionControlStopRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlOperationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/computer-owner": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Resolve a managed conversation owner for native desktop approval
         * @description Available only on the authenticated desktop-local server with a private computer bridge. Rejects browser Origin. Resolves identity from server context and Session Control ownership; does not create or approve computer sessions.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Namespace-local session key */
                    id: string;
                    /** @description Managed session namespace */
                    source: "tenant";
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerComputerOwnerResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/conversation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Read managed conversation events
         * @description Reads ordered main-run events by session key with an exclusive cursor. Ownership is checked before returning content.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Exclusive event cursor */
                    cursor?: string;
                };
                header?: never;
                path: {
                    /** @description Session key (not numeric database ID) */
                    id: string;
                    /** @description Session source */
                    source: "tenant";
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionConversationResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/events/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream bounded Session Control state
         * @description SSE event names are operation, run, or session. data is SwaggerSessionControlStateEvent only: no content, prompt, task result, Handoff body, audit metadata, credentials, attachment metadata, source locator, or secret. Auth and target readback complete before headers. Disconnect closes polling but never cancels a Run.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Resume cursor; overrides Last-Event-ID */
                    cursor?: string;
                    /** @description Maximum task events per poll */
                    limit?: number;
                };
                header: {
                    /** @description Last durable task-event cursor */
                    "Last-Event-ID"?: string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Namespace-local session key */
                    id: string;
                    /** @description Session namespace */
                    source: "tenant" | "local";
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlStateEvent"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/messages": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Send a message to a managed session
         * @description 向 tenant session 发送文字和图片，支持 inline_data base64 归档为会话隔离的持久附件。Local session 写入始终返回 forbidden；会话来源关系通过 source_refs 或 attachments/Attach 接口建立。
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Mutation idempotency key, at most 128 bytes */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Managed session key */
                    id: string;
                    /** @description Must be tenant */
                    source: "tenant";
                };
                cookie?: never;
            };
            /** @description Message request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSessionControlSendRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlOperationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Request Entity Too Large */
                413: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/monitors": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Schedule a managed session monitor
         * @description 为 tenant target 和 namespaced sources 建立监控。Local target 写入始终返回 forbidden；scheduler_unavailable 返回 503。
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Mutation idempotency key, at most 128 bytes */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Managed target session key */
                    id: string;
                    /** @description Must be tenant */
                    source: "tenant";
                };
                cookie?: never;
            };
            /** @description Monitor request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSessionControlMonitorRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlOperationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/session-control/sessions/{source}/{id}/stop": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Stop a managed session run
         * @description 停止 tenant session 的活动 Run。断开 SSE 不会调用此操作；Local session 写入始终返回 forbidden。
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Mutation idempotency key, at most 128 bytes */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Managed session key */
                    id: string;
                    /** @description Must be tenant */
                    source: "tenant";
                };
                cookie?: never;
            };
            requestBody?: components["requestBodies"]["internal_server.SwaggerSessionControlStopRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlOperationResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSessionControlError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant sessions */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListSessionsResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Upsert tenant session */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantSessionRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/sessions/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant session */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantSession"];
                    };
                };
            };
        };
        /** Replace tenant session fields */
        put: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantSessionRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        post?: never;
        /** Archive tenant session */
        delete: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerArchiveResponse"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        /** Update tenant session */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantSessionRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/sessions/{id}/images": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List generated images for a tenant session */
        get: {
            parameters: {
                query?: {
                    /** @description Maximum records */
                    limit?: number;
                };
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerImageGenerationListResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/sessions/{id}/images/edits": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Edit or redraw an image for a tenant session */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody: {
                content: {
                    "multipart/form-data": {
                        /**
                         * Format: binary
                         * @description Source image
                         */
                        image?: string;
                        /** @description Edit prompt */
                        prompt: string;
                        /** @description Existing source asset ID */
                        source_asset_id?: string;
                    };
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerImageArtifactResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Bad Gateway */
                502: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Gateway Timeout */
                504: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/sessions/{id}/images/generations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Generate an image for a tenant session */
        post: {
            parameters: {
                query?: never;
                header?: never;
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            /** @description Image generation request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerImageGenerateRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerImageArtifactResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Bad Gateway */
                502: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Gateway Timeout */
                504: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/sessions/{id}/timeline": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get tenant session full-chain timeline
         * @description Owner/admin endpoint that aggregates a session, messages, trace-linked audit logs, telemetry events, sub-agent tasks, and task events into one time-ordered timeline.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Message/task event limit */
                    limit?: number;
                    /** @description Sub-agent task scan limit */
                    task_limit?: number;
                    /** @description Audit/telemetry per-trace limit */
                    trace_limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Session ID */
                    id: number;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantSessionTimelineResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skill-overrides": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant skill overrides */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                    /** @description Skill key. When present, returns one override instead of list. */
                    skill_key?: string;
                    /** @description Skill version for detail lookup */
                    version?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListSkillOverridesResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Upsert tenant skill override */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Skill override request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantSkillOverrideRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skill-packages/import": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Import a tenant skill package without publishing it
         * @description Validates a local source_path on the server or a base64-encoded package.skill.zip and returns deterministic runtime.md plus manifest metadata without writing a tenant skill version.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantSkillPackageRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantSkillPackageResult"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skill-packages/publish": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Publish a tenant skill package as a tenant skill version
         * @description Stores package.skill.zip, manifest.json, and runtime.md in the local artifact store, then writes compiled runtime markdown and package metadata into tenant_skills.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantSkillPackageRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantSkillPackageResult"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skill-packages/render": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Render a tenant skill package without publishing it
         * @description Accepts a local source_path on the server or a base64-encoded package.skill.zip and returns deterministic runtime.md plus manifest metadata.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantSkillPackageRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantSkillPackageResult"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skill-packages/verify-runtime": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Verify tenant skill package runtime metadata
         * @description Runs a generic structured runtime smoke for a selected tenant skill and verifies safe tenant_runtime metadata such as loaded key, version, package hash, and loaded bytes. This endpoint does not validate upper-layer business state.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Skill package runtime verify request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSkillPackageVerifyRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSkillPackageVerifyResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skills": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant skills */
        get: {
            parameters: {
                query?: {
                    /** @description Only enabled skills */
                    enabled?: boolean;
                    /** @description Limit */
                    limit?: number;
                    /** @description Skill key. When present, returns one skill instead of list. */
                    skill_key?: string;
                    /** @description Skill version for detail lookup */
                    version?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListSkillsResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Upsert tenant skill */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Skill request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantSkillRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/skills/rollback": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Roll back tenant skill by copying a historical version into a new latest version */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Skill rollback request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerSkillRollbackRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSkillRollbackResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/team-memory": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant team memory */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListMemoriesResponse"];
                    };
                };
            };
        };
        put?: never;
        /** Save tenant team memory */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Memory request. Category is forced to team. */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTenantMemoryRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/telemetry": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant telemetry events */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search event name, category, source, status, trace id, resource, model, or tool */
                    search?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListTelemetryEventsResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Record a tenant telemetry event */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Telemetry event */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerTelemetryEventRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/tenants": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenants */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search tenant key, name, or status */
                    search?: string;
                };
                header: {
                    /** @description Current tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListTenantsResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Create or update a tenant */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Current tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /** Archive a tenant */
        delete: {
            parameters: {
                query: {
                    /** @description Tenant key to archive */
                    tenant_key: string;
                };
                header: {
                    /** @description Current tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerArchiveResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        /** Patch a tenant */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Current tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/usage/daily": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant daily token usage */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search source or model */
                    search?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListTenantUsageDailyResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/usage/ledger": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant usage ledger records */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search request id, trace id, source, route, model, status, or error */
                    search?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListTenantUsageLedgerResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/user": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get current tenant user */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTenantUser"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Create or update current tenant user */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantUserRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        /** Patch current tenant user */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantUserRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant users */
        get: {
            parameters: {
                query?: {
                    /** @description Offset cursor returned by the previous page */
                    cursor?: number;
                    /** @description Limit */
                    limit?: number;
                    /** @description Search user key, email, display name, role, or status */
                    search?: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListUsersResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /** Create or update a tenant user */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantUserRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        /** Archive a tenant user */
        delete: {
            parameters: {
                query: {
                    /** @description User key to archive */
                    user_key: string;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerArchiveResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        options?: never;
        head?: never;
        /** Patch a tenant user */
        patch: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description Admin user key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody: components["requestBodies"]["internal_server.SwaggerTenantUserRequest"];
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerIDResponse"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        trace?: never;
    };
    "/tenant/web-agent/conversations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List Web Agent conversations grouped by tenant session */
        get: {
            parameters: {
                query?: {
                    /** @description Limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerListWebAgentConversationsResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/web-agent/conversations/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get Web Agent conversation detail */
        get: {
            parameters: {
                query?: {
                    /** @description Per-run event limit */
                    event_limit?: number;
                    /** @description Task limit within session; legacy conversation scan limit */
                    limit?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User key */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Conversation ID, for example session:23 or legacy:13 */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWebAgentConversationDetail"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflow-runs/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Read an authoritative persisted run snapshot
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Run ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowRun"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflow-runs/{id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Request run cancellation and bounded executor cleanup
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Run ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowRun"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflow-runs/{id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Read ordered durable execution events
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Last applied event sequence */
                    after_sequence?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Run ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowEvents"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflow-runs/{id}/events/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream ordered execution events with persisted replay
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Last applied event sequence */
                    after_sequence?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Run ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Event"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/event-stream": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflow-runs/{id}/recover": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Explicitly recover an interrupted run using its checkpoint revision
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Run ID */
                    id: string;
                };
                cookie?: never;
            };
            /** @description Expected interrupted snapshot revision */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.SwaggerWorkflowRecovery"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowRun"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflows": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List private Team workflow versions
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowTemplates"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /**
         * Save a new immutable Team workflow version
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description Workflow with published Profile bindings */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.SaveRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowTemplate"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflows/{key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get a Team workflow version
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Fixed version; omit for latest */
                    version?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowTemplate"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflows/{key}/runs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List persisted runs for a Team
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Fixed Team version */
                    version?: number;
                };
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowRuns"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        /**
         * Queue an idempotent bounded Team run
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Unique submission key; reuse with identical body to replay */
                    "Idempotency-Key": string;
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Team key */
                    key: string;
                };
                cookie?: never;
            };
            /** @description Fixed Team version and task objective */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.StartRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowRun"];
                    };
                };
                /** @description Accepted */
                202: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowRun"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflows/templates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List beginner Team workflow starting points
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        get: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowBuiltins"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tenant/workflows/templates/{key}/create": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create a personal Team and Agents from a starting point
         * @description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
         */
        post: {
            parameters: {
                query?: never;
                header: {
                    /** @description Tenant key */
                    "X-Tenant-Key": string;
                    /** @description User identity */
                    "X-User-Id": string;
                };
                path: {
                    /** @description Built-in template key */
                    key: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerWorkflowTemplate"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Conflict */
                409: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/tools": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Tool definitions snapshot */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerSnapshotResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/trace": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Open the built-in trace viewer WebUI
         * @description Serves a local WebUI that visualizes local TUI/CLI transcripts and tenant API Server timelines.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description HTML trace viewer */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/trace/api/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List trace viewer sessions
         * @description Lists local transcript sessions by default, or tenant sessions when source=tenant and tenant headers are present.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Filter sessions updated at or after this RFC3339 timestamp */
                    from?: string;
                    /** @description Limit */
                    limit?: number;
                    /** @description Session source: local or tenant */
                    source?: string;
                    /** @description Filter sessions updated at or before this RFC3339 timestamp */
                    to?: string;
                };
                header?: {
                    /** @description Tenant key, required for source=tenant */
                    "X-Tenant-Key"?: string;
                    /** @description User key, required for source=tenant */
                    "X-User-Id"?: string;
                };
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTraceSessionsResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/trace/api/sessions/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get a trace viewer session detail
         * @description Returns a normalized summary, spans, and events for a local transcript session or tenant session timeline.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Message/task event limit */
                    limit?: number;
                    /** @description Session source: local or tenant */
                    source?: string;
                    /** @description Sub-agent task scan limit for tenant source */
                    task_limit?: number;
                    /** @description Audit/telemetry per-trace limit for tenant source */
                    trace_limit?: number;
                };
                header?: {
                    /** @description Tenant key, required for source=tenant */
                    "X-Tenant-Key"?: string;
                    /** @description User key, required for source=tenant */
                    "X-User-Id"?: string;
                };
                path: {
                    /** @description Local session UUID or tenant numeric session ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerTraceDetailResponse"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/trace/api/sessions/{id}/export": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Export a runtime trace artifact
         * @description Exports a metadata-only runtime-trace-v1 artifact for APG and offline baseline analysis.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Artifact schema (runtime-trace-v1) */
                    schema?: string;
                    /** @description Session source: local or tenant */
                    source?: string;
                };
                header?: {
                    /** @description Tenant key, required for source=tenant */
                    "X-Tenant-Key"?: string;
                    /** @description User key, required for source=tenant */
                    "X-User-Id"?: string;
                };
                path: {
                    /** @description Local session UUID or tenant numeric session ID */
                    id: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.RuntimeTraceArtifact"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Forbidden */
                403: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Not Found */
                404: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Service Unavailable */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/chat/completions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Create an OpenAI-compatible chat completion */
        post: {
            parameters: {
                query?: never;
                header?: {
                    /** @description Comma-separated tenant skill keys for structured JSON schema fast path; overrides metadata, tenant settings, env routes, and compatibility fallback */
                    "X-Tenant-Skill-Key"?: string;
                };
                path?: never;
                cookie?: never;
            };
            /** @description OpenAI-compatible chat request */
            requestBody: {
                content: {
                    "application/json": components["schemas"]["internal_server.OpenAIChatRequest"];
                };
            };
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerOpenAIChatResponse"];
                    };
                };
                /** @description Bad Request */
                400: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
                /** @description Internal Server Error */
                500: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/models": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List OpenAI-compatible models
         * @description Lists the models this deployment can actually serve: the configured providers' models, falling back to the built-in Anthropic list only when nothing is configured. It is not a fixed `claude-*` list.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerOpenAIModelsResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/providers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List configured providers
         * @description Names and default models of the providers this deployment is configured with. Credentials are never included.
         */
        get: {
            parameters: {
                query?: never;
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description OK */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerProviderListResponse"];
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "application/json": components["schemas"]["internal_server.SwaggerError"];
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/webui": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Open the WebUI
         * @description Redirects to /webui/. Registered unconditionally so an unbuilt frontend answers with build instructions instead of a bare 404.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Auth token; accepted once and stored in a /webui/-scoped cookie */
                    token?: string;
                };
                header?: never;
                path?: never;
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description Redirect to /webui/ */
                302: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/webui/{path}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Serve the WebUI single-page app
         * @description Serves the Vite build from NIUMA_WEBUI_DIR, or an auto-discovered web/dist next to the working directory or executable. Unknown paths fall back to index.html for client-side routing. When no build is present, responds 503 with a page explaining how to build the frontend.
         */
        get: {
            parameters: {
                query?: {
                    /** @description Auth token; accepted once and stored in a /webui/-scoped cookie */
                    token?: string;
                };
                header?: never;
                path: {
                    /** @description Asset path within the build, empty for index.html */
                    path: string;
                };
                cookie?: never;
            };
            requestBody?: never;
            responses: {
                /** @description WebUI asset */
                200: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
                /** @description Unauthorized */
                401: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
                /** @description Method not allowed */
                405: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
                /** @description Frontend not built; response body explains how to build it */
                503: {
                    headers: {
                        [name: string]: unknown;
                    };
                    content: {
                        "text/html": string;
                    };
                };
            };
        };
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        "github_com_konglong87_niuma_internal_agentprofile.BlockedOverride": {
            field?: string;
            reason?: string;
        };
        "github_com_konglong87_niuma_internal_agentprofile.CapabilityPolicy": {
            allow_agents?: boolean;
            allow_attachments?: boolean;
            mcp_servers?: string[];
            skills?: string[];
            tools?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ToolPolicy"];
        };
        "github_com_konglong87_niuma_internal_agentprofile.ContextPolicy": {
            git?: boolean;
            knowledge_base?: boolean;
            session_history?: boolean;
            tenant_memory?: boolean;
            user_memory?: boolean;
            workspace?: boolean;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentprofile.ExecutionKind": "text" | "computer" | "image";
        "github_com_konglong87_niuma_internal_agentprofile.ExecutionKindDescriptor": {
            display_name?: string;
            kind?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ExecutionKind"];
            workflow_supported?: boolean;
        };
        "github_com_konglong87_niuma_internal_agentprofile.ExecutionPolicy": {
            auto_compact?: boolean;
            effort?: string;
            kind?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ExecutionKind"];
            max_parallel_read_only_tools?: number;
            max_tokens?: number;
            max_turns?: number;
            model?: string;
            provider?: string;
            runtime_profile?: string;
        };
        "github_com_konglong87_niuma_internal_agentprofile.IdentityPolicy": {
            description?: string;
            display_name?: string;
            role_type?: string;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentprofile.PermissionMode": "ask" | "deny" | "allow" | "bypassPermissions";
        "github_com_konglong87_niuma_internal_agentprofile.Profile": {
            config?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ProfileDocument"];
            description?: string;
            display_name?: string;
            effective_hash?: string;
            id?: number;
            owner_user_id?: number;
            profile_key?: string;
            profile_version?: number;
            published_at?: string;
            requested_hash?: string;
            scope?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.Scope"];
            status?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.Status"];
            tenant_id?: number;
        };
        "github_com_konglong87_niuma_internal_agentprofile.ProfileDocument": {
            capabilities?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.CapabilityPolicy"];
            context?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ContextPolicy"];
            execution?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ExecutionPolicy"];
            identity?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.IdentityPolicy"];
            prompt?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.PromptPolicy"];
            safety?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.SafetyPolicy"];
            schema_version?: number;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentprofile.PromptMode": "code" | "chat";
        "github_com_konglong87_niuma_internal_agentprofile.PromptPolicy": {
            language?: string;
            mode?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.PromptMode"];
            output_style?: string;
            persona?: string;
            system_addendum?: string;
        };
        "github_com_konglong87_niuma_internal_agentprofile.RoleTypeDescriptor": {
            description?: string;
            display_name?: string;
            key?: string;
        };
        "github_com_konglong87_niuma_internal_agentprofile.SafetyPolicy": {
            allow_unsandboxed_commands?: boolean;
            permission_mode?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.PermissionMode"];
            sandbox?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.SandboxMode"];
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentprofile.SandboxMode": "required" | "optional" | "disabled";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentprofile.Scope": "builtin" | "tenant_shared" | "user_private";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentprofile.Status": "draft" | "validating" | "published" | "archived";
        "github_com_konglong87_niuma_internal_agentprofile.ToolPolicy": {
            allow?: string[];
            deny?: string[];
        };
        "github_com_konglong87_niuma_internal_agenttasks.Attachment": {
            attachment_id?: string;
            media_type?: string;
            name?: string;
            sha256?: string;
            size_bytes?: number;
            type?: string;
            url?: string;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Agent": {
            effort?: string;
            kind?: string;
            max_output_tokens?: number;
            model?: string;
            name?: string;
            profile_hash?: string;
            profile_id?: number;
            profile_key?: string;
            profile_version?: number;
            provider?: string;
            ref?: string;
            role_type?: string;
            route_hash?: string;
            system_prompt?: string;
            tools?: string[];
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Artifact": {
            attempt_no?: number;
            content?: string;
            created_at?: string;
            id?: string;
            kind?: string;
            node_id?: string;
            sha256?: string;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Attempt": {
            /** @description BudgetReserved is the immutable admission reservation, not provider usage. */
            budget_reserved?: number;
            epoch?: number;
            failure?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Failure"];
            finished_at?: string;
            model?: string;
            number?: number;
            provider?: string;
            started_at?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Status"];
            task_id?: number;
            usage?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Usage"];
            usage_known?: boolean;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Definition": {
            limits?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Limits"];
            nodes?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Node"][];
            schema_version?: number;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Event": {
            attempt_no?: number;
            created_at?: string;
            failure?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Failure"];
            node_id?: string;
            sequence?: number;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Status"];
            text?: string;
            type?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.EventType"];
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentteam_workflow.EventType": "run_queued" | "run_started" | "task_created" | "task_waiting_dependency" | "agent_started" | "agent_output_delta" | "agent_phase" | "agent_completed" | "agent_failed" | "task_retry_scheduled" | "run_cancel_requested" | "run_cancelled" | "task_cancelled" | "run_completed" | "run_failed" | "run_timed_out" | "run_interrupted" | "run_recovered";
        "github_com_konglong87_niuma_internal_agentteam_workflow.Failure": {
            code?: string;
            message?: string;
            retryable?: boolean;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Limits": {
            max_parallel?: number;
            max_total_tokens?: number;
            run_timeout_seconds?: number;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Node": {
            agent_ref?: string;
            depends_on?: string[];
            id?: string;
            instruction?: string;
            retry?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.RetryPolicy"];
            timeout_seconds?: number;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.NodeRun": {
            artifact_id?: string;
            attempts?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Attempt"][];
            failure?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Failure"];
            next_retry_at?: string;
            node_id?: string;
            output?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Status"];
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Plan": {
            agents?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Agent"][];
            definition?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Definition"];
            hash?: string;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.RetryPolicy": {
            backoff_ms?: number;
            max_attempts?: number;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Run": {
            artifacts?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Artifact"][];
            cancel_requested_at?: string;
            created_at?: string;
            deadline?: string;
            epoch?: number;
            failure?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Failure"];
            finished_at?: string;
            id?: string;
            input?: string;
            last_sequence?: number;
            lease_owner?: string;
            lease_until?: string;
            name?: string;
            nodes?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.NodeRun"][];
            plan?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Plan"];
            revision?: number;
            scope?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Scope"];
            started_at?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Status"];
            team_id?: number;
            team_key?: string;
            team_version?: number;
            /**
             * @description UncertainTokens includes in-flight and unconfirmed attempt reservations.
             *     It is not actual usage or a monetary amount.
             */
            uncertain_tokens?: number;
            usage?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Usage"];
        };
        "github_com_konglong87_niuma_internal_agentteam_workflow.Scope": {
            tenant_id?: number;
            user_id?: number;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_agentteam_workflow.Status": "queued" | "pending" | "ready" | "running" | "retry_wait" | "completed" | "failed" | "timed_out" | "cancel_requested" | "cancelled" | "interrupted" | "blocked";
        "github_com_konglong87_niuma_internal_agentteam_workflow.Usage": {
            cache_read_tokens?: number;
            cache_write_tokens?: number;
            input_tokens?: number;
            output_tokens?: number;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflowapp.Binding": {
            profile_id?: number;
            profile_key?: string;
            profile_version?: number;
            ref?: string;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflowapp.Builtin": {
            description?: string;
            key?: string;
            name?: string;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflowapp.SaveRequest": {
            agents?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.Binding"][];
            definition?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Definition"];
            description?: string;
            key?: string;
            name?: string;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflowapp.StartRequest": {
            input?: string;
            version?: number;
        };
        "github_com_konglong87_niuma_internal_agentteam_workflowapp.Template": {
            agents?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.Binding"][];
            definition?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Definition"];
            description?: string;
            id?: number;
            key?: string;
            name?: string;
            version?: number;
        };
        "github_com_konglong87_niuma_internal_buildinfo.Info": {
            build_time?: string;
            dirty?: boolean;
            dirty_known?: boolean;
            go_toolchain?: string;
            product?: string;
            revision?: string;
            schema_version?: string;
            version?: string;
        };
        "github_com_konglong87_niuma_internal_config.HookCommand": {
            command?: string;
            matcher?: string;
            tool?: string;
            tools?: string[];
        };
        "github_com_konglong87_niuma_internal_config.ImageModelCapability": {
            aspectRatios?: string[];
            maxInputImages?: number;
            operations?: string[];
            outputFormats?: string[];
            qualityOptions?: string[];
            resolutions?: string[];
            responseModes?: string[];
            sizes?: string[];
            supportsMask?: boolean;
            supportsWatermark?: boolean;
        };
        "github_com_konglong87_niuma_internal_config.SettingsIssue": {
            code?: string;
            field?: string;
            message?: string;
        };
        "github_com_konglong87_niuma_internal_files.Metadata": {
            gid?: number;
            mtime_known?: boolean;
            mtime_unix_nano?: number;
            owner_known?: boolean;
            uid?: number;
            /** @description name -> base64(value) */
            xattrs?: {
                [key: string]: string;
            };
            xattrs_known?: boolean;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.CriterionStatus": "pending" | "passed" | "failed" | "waived";
        "github_com_konglong87_niuma_internal_goal.Decision": {
            blocker_key?: string;
            confidence?: number;
            next_action?: string;
            reason?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.DecisionStatus"];
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.DecisionStatus": "continue" | "complete" | "blocked" | "failed";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.DependencyStatus": "unknown" | "available" | "missing" | "blocked";
        "github_com_konglong87_niuma_internal_goal.Event": {
            blocker_key?: string;
            checkpoint?: string;
            created_at?: string;
            duration_ms?: number;
            error?: string;
            goal_id?: string;
            id?: string;
            input_tokens?: number;
            message?: string;
            next_action?: string;
            output_tokens?: number;
            reason?: string;
            session_id?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Status"];
            turn?: number;
            type?: components["schemas"]["github_com_konglong87_niuma_internal_goal.EventType"];
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.EventType": "goal_started" | "goal_stopped" | "goal_resumed" | "turn_started" | "turn_finished" | "turn_failed" | "status_changed";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.EvidenceType": "command" | "test" | "git" | "api" | "db" | "doc" | "artifact" | "manual";
        "github_com_konglong87_niuma_internal_goal.Goal": {
            created_at?: string;
            cwd?: string;
            error?: string;
            id?: string;
            input_tokens?: number;
            last_blocker?: string;
            last_checkpoint?: string;
            last_next_action?: string;
            last_reason?: string;
            model?: string;
            objective?: string;
            output_tokens?: number;
            provider?: string;
            repeated_blocker_count?: number;
            session_id?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Status"];
            token_budget?: number;
            turn_budget?: number;
            turns_used?: number;
            updated_at?: string;
        };
        "github_com_konglong87_niuma_internal_goal.GoalCriterion": {
            description?: string;
            evidence_ids?: string[];
            id?: string;
            required?: boolean;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.CriterionStatus"];
        };
        "github_com_konglong87_niuma_internal_goal.GoalDependency": {
            description?: string;
            evidence_ids?: string[];
            id?: string;
            required?: boolean;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.DependencyStatus"];
            type?: string;
        };
        "github_com_konglong87_niuma_internal_goal.GoalEvidence": {
            command?: string;
            created_at?: string;
            exit_code?: number;
            goal_id?: string;
            id?: string;
            passed?: boolean;
            payload?: number[];
            summary?: string;
            type?: components["schemas"]["github_com_konglong87_niuma_internal_goal.EvidenceType"];
        };
        "github_com_konglong87_niuma_internal_goal.GoalRisk": {
            description?: string;
            id?: string;
            mitigation?: string;
            severity?: components["schemas"]["github_com_konglong87_niuma_internal_goal.RiskSeverity"];
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.RiskStatus"];
            type?: string;
        };
        "github_com_konglong87_niuma_internal_goal.GoalStep": {
            depends_on?: string[];
            evidence_ids?: string[];
            id?: string;
            rationale?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.StepStatus"];
            title?: string;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.RiskSeverity": "low" | "medium" | "high" | "critical";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.RiskStatus": "open" | "mitigated" | "accepted" | "escalated";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.Status": "active" | "complete" | "blocked" | "stopped" | "failed";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_goal.StepStatus": "pending" | "active" | "done" | "blocked" | "skipped";
        "github_com_konglong87_niuma_internal_imagegen.Artifact": {
            asset_id?: string;
            created_at?: string;
            generation_id?: string;
            height?: number;
            media_type?: string;
            model?: string;
            name?: string;
            operation?: string;
            provider?: string;
            session_id?: number;
            sha256?: string;
            size_bytes?: number;
            source_asset_id?: string;
            tenant_id?: number;
            url?: string;
            user_id?: number;
            width?: number;
        };
        "github_com_konglong87_niuma_internal_imagegen.GenerationRecord": {
            asset_id?: string;
            attempts?: number;
            batch_id?: string;
            cancel_requested_at?: string;
            created_at?: string;
            error_class?: string;
            error_code?: string;
            error_message?: string;
            finished_at?: string;
            generation_id?: string;
            heartbeat_at?: string;
            id?: number;
            idempotency_key?: string;
            lease_owner?: string;
            lease_until?: string;
            max_attempts?: number;
            model?: string;
            next_attempt_at?: string;
            operation?: string;
            origin_ref_json?: string;
            origin_type?: string;
            prompt?: string;
            provider?: string;
            provider_request_id?: string;
            request_json?: string;
            retry_of_generation_id?: string;
            session_id?: number;
            source_asset_id?: string;
            started_at?: string;
            status?: string;
            tenant_id?: number;
            tool_use_id?: string;
            trace_id?: string;
            updated_at?: string;
            user_id?: number;
        };
        "github_com_konglong87_niuma_internal_pendinginput.PendingInput": {
            attachments?: components["schemas"]["github_com_konglong87_niuma_internal_agenttasks.Attachment"][];
            attempt?: number;
            base_task_id?: number;
            claimed_at?: string;
            client_input_id?: string;
            content?: string;
            created_at?: string;
            direction?: string;
            dispatched_task_id?: number;
            error_code?: string;
            error_message?: string;
            id?: string;
            sequence?: number;
            session_id?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_pendinginput.Status"];
            tenant_id?: number;
            updated_at?: string;
            user_id?: number;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_pendinginput.Status": "queued" | "running" | "sent" | "failed" | "cancelled";
        "github_com_konglong87_niuma_internal_promptdump.CacheDiagnosticMessageBlock": {
            block_index?: number;
            bytes?: number;
            has_cache_control?: boolean;
            message_index?: number;
            role?: string;
            tool_name?: string;
            tool_use_id?: string;
            type?: string;
        };
        "github_com_konglong87_niuma_internal_promptdump.CacheDiagnosticSystemBlock": {
            has_cache_control?: boolean;
            index?: number;
            kind?: string;
            seen_count?: number;
            source?: string;
            stable?: boolean;
            text_bytes?: number;
            text_hash?: string;
        };
        "github_com_konglong87_niuma_internal_promptdump.CacheRecordDiagnostics": {
            cache_control_block_count?: number;
            cacheable_system_bytes?: number;
            largest_message_blocks?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.CacheDiagnosticMessageBlock"][];
            largest_uncached_system_blocks?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.CacheDiagnosticSystemBlock"][];
            message_cache_marker_count?: number;
            prefix_before_skills_hash?: string;
            prefix_through_skills_hash?: string;
            request_signature_delta_from_previous?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.CacheSignatureDelta"];
            request_signature_hash?: string;
            session_id?: string;
            skills_catalog_bytes?: number;
            skills_catalog_hash?: string;
            skills_catalog_position?: number;
            turn?: number;
            uncached_system_bytes?: number;
        };
        "github_com_konglong87_niuma_internal_promptdump.CacheSessionDiagnostics": {
            cache_control_hash_stable?: boolean;
            cacheable_system_bytes_max?: number;
            largest_uncached_system_blocks?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.CacheDiagnosticSystemBlock"][];
            message_prefix_hash_stable?: boolean;
            provider_cache_hit_ratio_input?: number;
            provider_cache_hit_ratio_total_input?: number;
            provider_cache_usage_state?: string;
            records?: number;
            request_signature_changed_fields?: string[];
            request_signature_changed_fields_count?: {
                [key: string]: number;
            };
            session_id?: string;
            system_hash_stable?: boolean;
            thinking_hash_stable?: boolean;
            tools_hash_stable?: boolean;
            uncached_system_bytes_max?: number;
        };
        "github_com_konglong87_niuma_internal_promptdump.CacheSignatureDelta": {
            breaks_cache?: boolean;
            cache_control_changed?: boolean;
            changed_fields?: string[];
            initialized?: boolean;
            message_prefix_changed?: boolean;
            model_changed?: boolean;
            previous_signature_hash?: string;
            signature_hash?: string;
            system_changed?: boolean;
            thinking_changed?: boolean;
            tools_changed?: boolean;
        };
        "github_com_konglong87_niuma_internal_promptdump.ContentBlockSummary": {
            actual_truncated?: boolean;
            connector_text_bytes?: number;
            content_bytes?: number;
            content_hash?: string;
            has_cache_control?: boolean;
            image_media_type?: string;
            image_source_type?: string;
            input_bytes?: number;
            input_hash?: string;
            is_error?: boolean;
            persisted_output?: boolean;
            raw_over_default_limit?: boolean;
            raw_over_limit?: boolean;
            suspected_truncated?: boolean;
            text_bytes?: number;
            thinking_bytes?: number;
            tool_name?: string;
            tool_use_id?: string;
            type?: string;
        };
        "github_com_konglong87_niuma_internal_promptdump.MessageSummary": {
            block_count?: number;
            blocks?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.ContentBlockSummary"][];
            bytes?: number;
            role?: string;
        };
        "github_com_konglong87_niuma_internal_promptdump.RequestRedaction": {
            mode?: string;
            raw_request_included?: boolean;
            text_omitted?: boolean;
        };
        "github_com_konglong87_niuma_internal_promptdump.SystemBlockSummary": {
            cache_scope?: string;
            cache_ttl?: string;
            cache_type?: string;
            has_cache_control?: boolean;
            index?: number;
            kind?: string;
            source?: string;
            text_bytes?: number;
            text_hash?: string;
        };
        "github_com_konglong87_niuma_internal_promptdump.ToolSummary": {
            description_bytes?: number;
            input_schema_bytes?: number;
            input_schema_fields?: string[];
            input_schema_hash?: string;
            name?: string;
        };
        "github_com_konglong87_niuma_internal_query.ComputerObservationReference": {
            asset_id?: string;
            media_type?: string;
            name?: string;
            observation_id?: string;
            sha256?: string;
            size_bytes?: number;
        };
        "github_com_konglong87_niuma_internal_query.ToolTrace": {
            computer_observation?: components["schemas"]["github_com_konglong87_niuma_internal_query.ComputerObservationReference"];
            file_changes?: components["schemas"]["github_com_konglong87_niuma_internal_tools.FileChange"][];
            id?: string;
            input?: string;
            is_error?: boolean;
            name?: string;
            output?: string;
        };
        "github_com_konglong87_niuma_internal_query.Usage": {
            cache_creation_ephemeral_1h_input_tokens?: number;
            cache_creation_ephemeral_5m_input_tokens?: number;
            cache_creation_input_tokens?: number;
            cache_read_input_tokens?: number;
            inference_geo?: string;
            input_tokens?: number;
            output_tokens?: number;
            service_tier?: string;
            speed?: string;
        };
        "github_com_konglong87_niuma_internal_scheduler.Event": {
            background_id?: string;
            created_at?: string;
            cwd?: string;
            error?: string;
            id?: string;
            offset?: number;
            prompt?: string;
            run_count?: number;
            schedule_id?: string;
            status?: string;
            type?: string;
        };
        "github_com_konglong87_niuma_internal_scheduler.RunRecord": {
            background_id?: string;
            cwd?: string;
            error?: string;
            finished_at?: string;
            id?: string;
            log_bytes?: number;
            prompt?: string;
            schedule_id?: string;
            started_at?: string;
            status?: string;
        };
        "github_com_konglong87_niuma_internal_sessioncontrol.HandoffCandidateRemoval": {
            category?: string;
            estimated_tokens?: number;
        };
        "github_com_konglong87_niuma_internal_sessioncontrol.SessionChannel": {
            account_key?: string;
            provider?: string;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_sessioncontrol.SessionStatus": "idle" | "queued" | "running" | "waiting_permission" | "waiting_input" | "blocked" | "completed" | "failed" | "stopped" | "archived";
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_sessioncontrol.Source": "tenant" | "local";
        "github_com_konglong87_niuma_internal_skills.Skill": {
            agent?: string;
            allowed_tools?: string[];
            argument_hint?: string;
            arguments?: string[];
            content?: string;
            description?: string;
            disable_model_invocation?: boolean;
            effort?: string;
            execution_context?: string;
            hooks?: {
                [key: string]: components["schemas"]["github_com_konglong87_niuma_internal_config.HookCommand"][];
            };
            legacy?: boolean;
            local_name?: string;
            model?: string;
            name?: string;
            package_ref?: string;
            package_sha256?: string;
            path?: string;
            paths?: string[];
            plugin?: string;
            root?: string;
            runtime_ref?: string;
            source?: components["schemas"]["github_com_konglong87_niuma_internal_skills.Source"];
            user_invocable?: boolean;
            version?: string;
            when_to_use?: string;
        };
        /** @enum {string} */
        "github_com_konglong87_niuma_internal_skills.Source": "user" | "project" | "plugin" | "bundled" | "marketplace" | "mcp" | "tenant";
        "github_com_konglong87_niuma_internal_storage_mysql.AgentProfile": {
            config_json?: string;
            created_at?: string;
            created_by_user_id?: number;
            description?: string;
            display_name?: string;
            effective_hash?: string;
            id?: number;
            owner_key?: string;
            owner_user_id?: number;
            profile_key?: string;
            profile_version?: number;
            published_at?: string;
            requested_hash?: string;
            scope?: string;
            source_kind?: string;
            source_path?: string;
            source_ref?: string;
            status?: string;
            tenant_id?: number;
            updated_at?: string;
            updated_by_user_id?: number;
            validation_json?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.AgentProfileConversationSummary": {
            account_id?: number;
            account_key?: string;
            chat_type?: string;
            conversation_id?: number;
            conversation_status?: string;
            external_chat_id?: string;
            external_thread_id?: string;
            last_inbound_at?: string;
            last_message_at?: string;
            last_message_preview?: string;
            last_message_role?: string;
            last_outbound_at?: string;
            latest_run_status?: string;
            message_count?: number;
            model?: string;
            run_count?: number;
            session_id?: number;
            title?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.AgentProfileTeamLink": {
            account_id?: number;
            account_key?: string;
            external_chat_id?: string;
            member_key?: string;
            role?: string;
            status?: string;
            team_display_name?: string;
            team_id?: number;
            team_key?: string;
            team_version?: number;
            trigger_policy?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.AgentTask": {
            agent_name?: string;
            description?: string;
            finished_at?: string;
            id?: number;
            idempotency_key?: string;
            metadata_json?: string;
            model?: string;
            parent_session_id?: number;
            result_json?: string;
            started_at?: string;
            status?: string;
            subagent_session_key?: string;
            tenant_id?: number;
            trace_id?: string;
            user_id?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent": {
            channel?: string;
            created_at?: string;
            event_type?: string;
            id?: number;
            payload_json?: string;
            source?: string;
            surface?: string;
            task_id?: number;
            trace_id?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.AuditLog": {
            action?: string;
            actor_user_id?: number;
            created_at?: string;
            id?: number;
            metadata_json?: string;
            resource_id?: string;
            resource_type?: string;
            tenant_id?: number;
            trace_id?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.Document": {
            active?: boolean;
            content_json?: string;
            content_md?: string;
            doc_type?: string;
            id?: number;
            title?: string;
            version?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.EffectiveSkill": {
            config_json?: string;
            content_md?: string;
            description?: string;
            enabled?: boolean;
            id?: number;
            manifest_json?: string;
            name?: string;
            override_id?: number;
            package_ref?: string;
            package_sha256?: string;
            runtime_ref?: string;
            skill_key?: string;
            updated_at?: string;
            version?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.KnowledgeChunk": {
            chunk_index?: number;
            content?: string;
            document_id?: number;
            embedding_ref?: string;
            id?: number;
            metadata_json?: string;
            score?: number;
            search_mode?: string;
            source_type?: string;
            title?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.KnowledgeDocument": {
            content?: string;
            id?: number;
            metadata_json?: string;
            source_type?: string;
            status?: string;
            tenant_id?: number;
            title?: string;
            updated_at?: string;
            user_id?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.Memory": {
            category?: string;
            content?: string;
            id?: number;
            importance?: number;
            memory_key?: string;
            metadata_json?: string;
            source?: string;
            updated_at?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.Message": {
            content?: string;
            content_json?: string;
            created_at?: string;
            id?: number;
            input_tokens?: number;
            is_error?: boolean;
            model?: string;
            output_tokens?: number;
            role?: string;
            session_id?: number;
            tool_id?: string;
            tool_name?: string;
            trace_id?: string;
            turn_index?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.QuotaEvent": {
            created_at?: string;
            current_value?: number;
            event_type?: string;
            id?: number;
            limit_type?: string;
            limit_value?: number;
            metadata_json?: string;
            model?: string;
            request_id?: string;
            route?: string;
            source?: string;
            tenant_id?: number;
            trace_id?: string;
            user_id?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.Session": {
            cwd?: string;
            id?: number;
            last_message_at?: string;
            model?: string;
            session_key?: string;
            started_at?: string;
            status?: string;
            title?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.Skill": {
            config_json?: string;
            content_md?: string;
            description?: string;
            enabled?: boolean;
            id?: number;
            manifest_json?: string;
            name?: string;
            package_ref?: string;
            package_sha256?: string;
            runtime_ref?: string;
            skill_key?: string;
            updated_at?: string;
            version?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.SkillOverride": {
            config_json?: string;
            enabled?: boolean;
            id?: number;
            name?: string;
            skill_id?: number;
            skill_key?: string;
            updated_at?: string;
            version?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.TelemetryEvent": {
            cache_creation_ephemeral_1h_input_tokens?: number;
            cache_creation_ephemeral_5m_input_tokens?: number;
            cache_creation_input_tokens?: number;
            cache_read_input_tokens?: number;
            category?: string;
            cli_session_id?: string;
            created_at?: string;
            duration_ms?: number;
            error?: string;
            id?: number;
            input_tokens?: number;
            model?: string;
            name?: string;
            occurred_at?: string;
            output_tokens?: number;
            parent_span_id?: string;
            properties?: {
                [key: string]: unknown;
            };
            request_purpose?: string;
            resource_id?: string;
            resource_type?: string;
            schema_version?: string;
            session_id?: number;
            source?: string;
            span_id?: string;
            started_at?: string;
            status?: string;
            tenant_id?: number;
            tenant_key?: string;
            tool_name?: string;
            trace_id?: string;
            turn_index?: number;
            user_id?: number;
            user_key?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.Tenant": {
            id?: number;
            name?: string;
            settings_json?: string;
            status?: string;
            tenant_key?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.UsageDaily": {
            cache_creation_input_tokens?: number;
            cache_read_input_tokens?: number;
            id?: number;
            input_tokens?: number;
            message_count?: number;
            model?: string;
            output_tokens?: number;
            rejected_count?: number;
            request_count?: number;
            source?: string;
            tenant_id?: number;
            total_tokens?: number;
            updated_at?: string;
            usage_date?: string;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.UsageLedger": {
            cache_creation_ephemeral_1h_input_tokens?: number;
            cache_creation_ephemeral_5m_input_tokens?: number;
            cache_creation_input_tokens?: number;
            cache_read_input_tokens?: number;
            created_at?: string;
            error_code?: string;
            error_message?: string;
            estimated?: boolean;
            finished_at?: string;
            id?: number;
            input_tokens?: number;
            model?: string;
            output_tokens?: number;
            provider?: string;
            request_id?: string;
            reserved_input_tokens?: number;
            reserved_output_tokens?: number;
            route?: string;
            session_id?: number;
            source?: string;
            started_at?: string;
            status?: string;
            tenant_id?: number;
            total_tokens?: number;
            trace_id?: string;
            turn?: number;
            updated_at?: string;
            usage_source?: string;
            user_id?: number;
        };
        "github_com_konglong87_niuma_internal_storage_mysql.User": {
            display_name?: string;
            email?: string;
            id?: number;
            metadata_json?: string;
            role?: string;
            status?: string;
            tenant_id?: number;
            updated_at?: string;
            user_info_json?: string;
            user_key?: string;
        };
        "github_com_konglong87_niuma_internal_tenantpkg.Manifest": {
            files?: components["schemas"]["github_com_konglong87_niuma_internal_tenantpkg.ManifestFile"][];
            package_sha256?: string;
            render?: components["schemas"]["github_com_konglong87_niuma_internal_tenantpkg.RenderInfo"];
            schema_version?: string;
            skill_key?: string;
        };
        "github_com_konglong87_niuma_internal_tenantpkg.ManifestFile": {
            path?: string;
            render_order?: number;
            runtime?: boolean;
            sha256?: string;
            size?: number;
        };
        "github_com_konglong87_niuma_internal_tenantpkg.RenderInfo": {
            entrypoint?: string;
            excluded_prefixes?: string[];
            runtime_excludes?: string[];
            runtime_files?: string[];
            runtime_includes?: string[];
        };
        "github_com_konglong87_niuma_internal_tools.FileChange": {
            after?: string;
            after_exists?: boolean;
            after_is_dir?: boolean;
            after_is_symlink?: boolean;
            after_link_target?: string;
            after_mode?: number;
            after_mode_known?: boolean;
            after_sha256?: string;
            after_snapshot_path?: string;
            before?: string;
            before_exists?: boolean;
            /**
             * @description Directory object type. Restore recreates/removes directories rather than
             *     treating them as regular files.
             */
            before_is_dir?: boolean;
            /**
             * @description Object-type fields describe symlinks so rewind can restore the link
             *     itself rather than following it. Regular files leave these empty.
             */
            before_is_symlink?: boolean;
            before_link_target?: string;
            /**
             * @description BeforeMetadata carries capability-aware filesystem metadata (mtime, owner,
             *     xattr) restored best-effort after content. A nil pointer means no metadata
             *     was captured; individual capabilities carry their own "known" flags.
             */
            before_metadata?: components["schemas"]["github_com_konglong87_niuma_internal_files.Metadata"];
            before_mode?: number;
            before_mode_known?: boolean;
            /**
             * @description BeforeSHA256 / AfterSHA256 record content hashes so a content-free
             *     transcript (file bodies externalized to the snapshot store) stays
             *     auditable: you can still tell what changed and compare versions without
             *     the transcript carrying the file body. Empty for empty/inline content.
             */
            before_sha256?: string;
            before_snapshot_path?: string;
            /**
             * @description BeforeSuperseded marks a turn-level lite entry: an earlier file_change in
             *     the same turn already holds the recoverable turn-start state for this path,
             *     so this entry keeps only path + hashes for observability and carries no
             *     restorable body. Rewind restores the turn-start state, so intermediate
             *     versions need no snapshot.
             */
            before_superseded?: boolean;
            /**
             * @description MessageID keys the change to the message/turn that produced it in a v2
             *     message-graph transcript (aligning with Claude Code's messageId->backup
             *     model). Empty for v1 linear transcripts. It is observability/interop only;
             *     rewind derives its restore set from the message-graph chain structure.
             */
            message_id?: string;
            mode_changed?: boolean;
            path?: string;
            /**
             * @description Source records which tool boundary captured the change (edit/write/bash)
             *     for observability; it never affects restore.
             */
            source?: string;
        };
        "internal_server.conversationSubscription": {
            cursor?: string;
            ref?: string;
        };
        "internal_server.EffectiveSettingsResponse": {
            activation?: components["schemas"]["internal_server.SettingsActivation"];
            file_resolved?: components["schemas"]["internal_server.SettingsFileResolved"];
            global_path?: string;
            process_snapshot?: components["schemas"]["internal_server.SettingsProcessSnapshot"];
            scope?: string;
            workspace?: string;
        };
        "internal_server.GlobalSettingsPromoteRequest": {
            doc?: {
                [key: string]: unknown;
            };
            provider_index?: number;
        };
        "internal_server.GlobalSettingsPromoteResponse": {
            doc?: {
                [key: string]: unknown;
            };
            masked?: string[];
            revision?: string;
        };
        "internal_server.GlobalSettingsResponse": {
            doc?: {
                [key: string]: unknown;
            };
            exists?: boolean;
            masked?: string[];
            path?: string;
            revision?: string;
        };
        "internal_server.GlobalSettingsSaveResponse": {
            path?: string;
            requires_restart?: boolean;
            revision?: string;
            saved?: boolean;
        };
        "internal_server.OpenAIChatRequest": {
            max_completion_tokens?: number;
            max_tokens?: number;
            messages?: components["schemas"]["internal_server.OpenAIMessage"][];
            metadata?: {
                [key: string]: unknown;
            };
            model?: string;
            profile_id?: string;
            profile_overrides?: {
                [key: string]: unknown;
            };
            profile_version?: number;
            response_format?: components["schemas"]["internal_server.OpenAIResponseFormat"];
            stream?: boolean;
            temperature?: number;
            top_p?: number;
            user?: string;
        };
        "internal_server.OpenAIFunctionCall": {
            arguments?: string;
            name?: string;
        };
        "internal_server.OpenAIMessage": {
            content?: number[];
            function_call?: components["schemas"]["internal_server.OpenAIFunctionCall"];
            name?: string;
            role?: string;
            tool_call_id?: string;
            tool_calls?: components["schemas"]["internal_server.OpenAIToolCall"][];
        };
        "internal_server.OpenAIResponseFormat": {
            json_schema?: components["schemas"]["internal_server.OpenAIResponseFormatSchema"];
            type?: string;
        };
        "internal_server.OpenAIResponseFormatSchema": {
            description?: string;
            name?: string;
            schema?: number[];
            strict?: boolean;
        };
        "internal_server.openAITenantRuntimeMetadata": {
            active?: boolean;
            bytes?: number;
            loaded_keys?: string[];
            package_refs?: string[];
            package_sha256?: string[];
            resolved?: boolean;
            runtime_refs?: string[];
            skill_keys?: string[];
            source?: string;
            tenant_addendum?: boolean;
            versions?: string[];
        };
        "internal_server.OpenAIToolCall": {
            function?: components["schemas"]["internal_server.OpenAIFunctionCall"];
            id?: string;
            type?: string;
        };
        "internal_server.QueryAttachment": {
            attachment_id?: string;
            inline_data?: string;
            media_type?: string;
            name?: string;
            sha256?: string;
            size_bytes?: number;
            transcript?: string;
            type?: string;
            url?: string;
        };
        "internal_server.QueryRequest": {
            attachments?: components["schemas"]["internal_server.QueryAttachment"][];
            cwd?: string;
            max_tokens?: number;
            model?: string;
            profile_blocked_override_count?: number;
            profile_effective_hash?: string;
            profile_id?: string;
            profile_overrides?: {
                [key: string]: unknown;
            };
            profile_requested_hash?: string;
            profile_source?: string;
            profile_surface?: string;
            profile_version?: number;
            prompt?: string;
            prompt_mode?: string;
            provider?: string;
            session_key?: string;
            system_prompt?: string;
        };
        "internal_server.RuntimeBackgroundJob": {
            created_at?: string;
            cwd?: string;
            enabled?: boolean;
            id?: string;
            interval_seconds?: number;
            kind?: string;
            last_error?: string;
            last_run_at?: string;
            log_path?: string;
            log_tail?: string;
            next_run_at?: string;
            pid?: number;
            prompt?: string;
            run_count?: number;
            schedule_id?: string;
            spec?: string;
            status?: string;
            updated_at?: string;
        };
        "internal_server.RuntimeTraceArtifact": {
            configuration?: components["schemas"]["internal_server.RuntimeTraceConfiguration"];
            diagnostics?: components["schemas"]["internal_server.RuntimeTraceDiagnostic"][];
            quality?: components["schemas"]["internal_server.RuntimeTraceQuality"];
            run?: components["schemas"]["internal_server.RuntimeTraceRun"];
            schema_version?: string;
            spans?: components["schemas"]["internal_server.RuntimeTraceArtifactSpan"][];
            summary?: components["schemas"]["internal_server.RuntimeTraceSummary"];
        };
        "internal_server.RuntimeTraceArtifactSpan": {
            concurrency_class?: string;
            duration_ms?: number;
            id?: string;
            model?: string;
            name?: string;
            parent_id?: string;
            self_duration_ms?: number;
            skill?: string;
            start?: string;
            status?: string;
            tool_name?: string;
            trace_id?: string;
            turn_index?: number;
            type?: string;
        };
        "internal_server.RuntimeTraceConfiguration": {
            effective_max_parallel_read_only_tools?: number;
            read_only_tool_mode?: components["schemas"]["internal_server.RuntimeTraceReadOnlyToolMode"];
            requested_max_parallel_read_only_tools?: number;
        };
        "internal_server.RuntimeTraceDiagnostic": {
            code?: string;
            count?: number;
            estimated_savings_ms?: number;
            fingerprint?: string;
            message?: string;
            severity?: string;
            span_ids?: string[];
            tool_name?: string;
            turn_index?: number;
        };
        "internal_server.RuntimeTraceQuality": {
            completion_verified?: boolean;
            failed_test_attempts?: number;
            final_verification_passed?: boolean;
            gate_blocks?: number;
            recoveries?: number;
            test_attempts?: number;
            test_failure_recovered?: boolean;
            tests_passed?: boolean;
            tests_run?: boolean;
            todo_writes?: number;
            tool_errors?: number;
        };
        /** @enum {string} */
        "internal_server.RuntimeTraceReadOnlyToolMode": "serial" | "parallel";
        "internal_server.RuntimeTraceRun": {
            agent?: string;
            agent_version?: string;
            build_info?: components["schemas"]["github_com_konglong87_niuma_internal_buildinfo.Info"];
            duration_ms?: number;
            run_id?: string;
            session_id?: string;
            started_at?: string;
            status?: components["schemas"]["internal_server.RuntimeTraceRunStatus"];
        };
        /** @enum {string} */
        "internal_server.RuntimeTraceRunStatus": "unknown" | "failed" | "completed";
        "internal_server.RuntimeTraceSummary": {
            agent_tasks?: number;
            cache_creation_tokens?: number;
            cache_hit_ratio?: number;
            cache_read_tokens?: number;
            cache_summary?: string;
            critical_path_ms?: number;
            duration_ms?: number;
            errors?: number;
            files_changed?: number;
            input_tokens?: number;
            messages?: number;
            model_wall_ms?: number;
            output_tokens?: number;
            parallel_savings_ms?: number;
            permission_decisions?: number;
            skills?: string[];
            task_wall_ms?: number;
            token_summary?: string;
            tool_calls?: number;
            tool_errors?: number;
            tool_wall_ms?: number;
            tool_work_ms?: number;
            total_model_duration_ms?: number;
            total_tokens?: number;
            total_tool_duration_ms?: number;
            turns?: number;
            unattributed_ms?: number;
        };
        "internal_server.sessionControlLinkDTO": {
            created_at?: string;
            id?: number;
            relation_type?: string;
            source?: string;
            target?: string;
        };
        "internal_server.sessionControlSessionDTO": {
            active_run_id?: number;
            channel?: components["schemas"]["github_com_konglong87_niuma_internal_sessioncontrol.SessionChannel"];
            cwd?: string;
            effort?: string;
            id?: number;
            links?: components["schemas"]["internal_server.sessionControlLinkDTO"][];
            model?: string;
            permission_mode?: string;
            prompt_mode?: string;
            provider?: string;
            read_only?: boolean;
            ref?: string;
            short_id?: string;
            source?: components["schemas"]["github_com_konglong87_niuma_internal_sessioncontrol.Source"];
            status?: components["schemas"]["github_com_konglong87_niuma_internal_sessioncontrol.SessionStatus"];
            title?: string;
            updated_at?: string;
        };
        "internal_server.sessionConversationPage": {
            cursor?: string;
            events?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent"][];
            has_more?: boolean;
            schema_version?: string;
            session?: components["schemas"]["internal_server.sessionControlSessionDTO"];
        };
        "internal_server.SettingsActivation": {
            existing_runs?: string;
            new_runs?: string;
            requires_restart?: boolean;
        };
        "internal_server.SettingsEnvironmentInfo": {
            api_path?: string;
            available?: boolean;
            database?: string;
            id?: string;
            label?: string;
            tenant_key?: string;
            user_id?: string;
        };
        "internal_server.SettingsEnvironmentsResponse": {
            environments?: components["schemas"]["internal_server.SettingsEnvironmentInfo"][];
            global_settings_path?: string;
            global_settings_shared?: boolean;
        };
        "internal_server.SettingsFileResolved": {
            doc?: {
                [key: string]: unknown;
            };
            includes_process_environment?: boolean;
            masked?: string[];
            route_sources?: {
                [key: string]: string;
            };
            sources?: string[];
        };
        "internal_server.SettingsProcessSnapshot": {
            active_run_config_known?: boolean;
            available?: boolean;
            doc?: {
                [key: string]: unknown;
            };
            kind?: string;
        };
        "internal_server.SettingsProviderTestRequest": {
            doc?: {
                [key: string]: unknown;
            };
            provider?: string;
        };
        "internal_server.SettingsProviderTestResponse": {
            kind?: string;
            message?: string;
            ok?: boolean;
            status_code?: number;
        };
        "internal_server.SettingsValidationResponse": {
            issues?: components["schemas"]["github_com_konglong87_niuma_internal_config.SettingsIssue"][];
            valid?: boolean;
        };
        "internal_server.SwaggerAgentProfile": {
            config_json?: string;
            created_at?: string;
            created_by_user_id?: number;
            description?: string;
            display_name?: string;
            effective_hash?: string;
            id?: number;
            owner_key?: string;
            owner_user_id?: number;
            profile_key?: string;
            profile_version?: number;
            published_at?: string;
            requested_hash?: string;
            scope?: string;
            source_kind?: string;
            source_path?: string;
            source_ref?: string;
            status?: string;
            tenant_id?: number;
            updated_at?: string;
            updated_by_user_id?: number;
            validation_json?: string;
        };
        "internal_server.SwaggerAgentProfileAssignment": {
            assigned_by_user_id?: number;
            created_at?: string;
            id?: number;
            profile_id?: number;
            surface?: string;
            tenant_id?: number;
            updated_at?: string;
            user_id?: number;
        };
        "internal_server.SwaggerAgentProfileChannelBinding": {
            account_id?: number;
            archived_at?: string;
            binding_key?: string;
            created_at?: string;
            created_by_user_id?: number;
            id?: number;
            profile_id?: number;
            provider?: string;
            status?: string;
            tenant_id?: number;
            updated_at?: string;
        };
        "internal_server.SwaggerAgentProfileConversationCatalog": {
            conversations?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentProfileConversationSummary"][];
            message_count?: number;
            profile?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentProfile"];
            run_count?: number;
            teams?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentProfileTeamLink"][];
        };
        "internal_server.SwaggerAgentSlashCommand": {
            /** @example Run a code review prompt */
            description?: string;
            /** @example review */
            name?: string;
            /** @example builtin */
            source?: string;
        };
        "internal_server.SwaggerAgentTaskAttachment": {
            /** @example att_123 */
            attachment_id?: string;
            /** @example iVBORw0KGgo... */
            inline_data?: string;
            /** @example image/png */
            media_type?: string;
            /** @example screenshot.png */
            name?: string;
            /** @example abc123 */
            sha256?: string;
            /** @example 1024 */
            size_bytes?: number;
            /** @example image */
            type?: string;
            /** @example https://cdn.example.test/screenshot.png */
            url?: string;
        };
        "internal_server.SwaggerAgentTaskMessageRequest": {
            attachments?: components["schemas"]["internal_server.SwaggerAgentTaskAttachment"][];
            content?: string;
            from_agent?: string;
            trace_id?: string;
        };
        "internal_server.SwaggerAgentTaskMessageResponse": {
            id?: number;
            /** @example running */
            status?: string;
            task_id?: number;
        };
        "internal_server.SwaggerAgentTaskPermissionRequest": {
            allowed?: boolean;
            destination?: string;
            reason?: string;
            rule?: string;
        };
        "internal_server.SwaggerAgentTaskPermissionResponse": {
            allowed?: boolean;
            id?: number;
            request_id?: string;
        };
        "internal_server.SwaggerAgentTaskQuestionAnswerRequest": {
            /** @example Architecture diagram */
            answer?: string;
        };
        "internal_server.SwaggerAgentTaskQuestionAnswerResponse": {
            /** @example Architecture diagram */
            answer?: string;
            /** @example 44 */
            id?: number;
            /** @example question-uuid */
            request_id?: string;
            /** @example answered */
            status?: string;
        };
        "internal_server.SwaggerAgentTaskResponse": {
            agent_name?: string;
            description?: string;
            finished_at?: string;
            id?: number;
            idempotency_key?: string;
            metadata_json?: string;
            model?: string;
            parent_session_id?: number;
            result_json?: string;
            started_at?: string;
            status?: string;
            subagent_session_key?: string;
            tenant_id?: number;
            trace_id?: string;
            user_id?: number;
        };
        "internal_server.SwaggerAgentTeam": {
            created_at?: string;
            created_by_user_id?: number;
            description?: string;
            display_name?: string;
            effective_hash?: string;
            id?: number;
            owner_key?: string;
            owner_user_id?: number;
            policy_json?: string;
            published_at?: string;
            requested_hash?: string;
            schema_version?: number;
            scope?: string;
            status?: string;
            team_key?: string;
            team_version?: number;
            tenant_id?: number;
            updated_at?: string;
            updated_by_user_id?: number;
            validation_json?: string;
        };
        "internal_server.SwaggerAgentTeamBinding": {
            account_id?: number;
            created_at?: string;
            external_chat_id?: string;
            external_thread_id?: string;
            id?: number;
            provider?: string;
            status?: string;
            team_id?: number;
            tenant_id?: number;
            trigger_policy?: string;
            updated_at?: string;
        };
        "internal_server.SwaggerAgentTeamMember": {
            account_id?: number;
            created_at?: string;
            execution_override_json?: string;
            id?: number;
            member_key?: string;
            profile_id?: number;
            role?: string;
            status?: string;
            team_id?: number;
            tenant_id?: number;
            tool_policy_json?: string;
            updated_at?: string;
            workspace_policy_json?: string;
        };
        "internal_server.SwaggerAgentTeamRun": {
            cancel_requested_at?: string;
            conversation_id?: number;
            coordinator_member_key?: string;
            created_at?: string;
            error_code?: string;
            error_message?: string;
            finished_at?: string;
            heartbeat_at?: string;
            id?: string;
            inbox_event_id?: number;
            max_parallel_members?: number;
            max_rounds?: number;
            max_total_tokens?: number;
            member_count?: number;
            result_json?: string;
            source_account_id?: number;
            source_kind?: string;
            started_at?: string;
            status?: string;
            team_effective_hash?: string;
            team_id?: number;
            tenant_id?: number;
            updated_at?: string;
            used_tokens?: number;
            used_turns?: number;
        };
        "internal_server.SwaggerAgentWorkspaceValidateRequest": {
            /** @example /Users/example/GolandProjects/niuma */
            cwd?: string;
        };
        "internal_server.SwaggerAgentWorkspaceValidateResponse": {
            cwd?: string;
            exists?: boolean;
            git_root?: string;
            is_dir?: boolean;
            is_git_repo?: boolean;
            workspace_name?: string;
        };
        "internal_server.SwaggerArchiveResponse": {
            archived?: boolean;
            id?: number;
        };
        "internal_server.SwaggerAutoMemoryReviewRequest": {
            action?: string;
            memory_key?: string;
        };
        "internal_server.SwaggerCancelAgentTaskResponse": {
            cancelled?: boolean;
            id?: number;
            in_process?: boolean;
        };
        "internal_server.SwaggerComputerOwnerResponse": {
            data?: {
                session_id?: number;
                tenant_id?: number;
                user_id?: number;
            };
        };
        "internal_server.SwaggerCreateAgentTaskRequest": {
            agent_name?: string;
            description?: string;
            metadata_json?: number[];
            model?: string;
            parent_session_id?: number;
            prompt?: string;
            result_json?: number[];
            /** @example running */
            status?: string;
            subagent_session_key?: string;
            trace_id?: string;
        };
        "internal_server.SwaggerCreateAgentTaskResponse": {
            id?: number;
        };
        "internal_server.SwaggerCreateGoalRequest": {
            cwd?: string;
            model?: string;
            objective?: string;
            session_id?: string;
            token_budget?: number;
            turn_budget?: number;
        };
        "internal_server.SwaggerEffectiveAgentProfile": {
            blocked_overrides?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.BlockedOverride"][];
            config?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ProfileDocument"];
            effective_hash?: string;
            profile?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.Profile"];
            profile_key?: string;
            profile_version?: number;
            requested?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ProfileDocument"];
            requested_hash?: string;
            source?: string;
        };
        "internal_server.SwaggerError": {
            error?: string;
        };
        "internal_server.SwaggerGoalPlanResponse": {
            acceptance_criteria?: components["schemas"]["github_com_konglong87_niuma_internal_goal.GoalCriterion"][];
            created_at?: string;
            current_step_id?: string;
            dependencies?: components["schemas"]["github_com_konglong87_niuma_internal_goal.GoalDependency"][];
            goal_id?: string;
            risks?: components["schemas"]["github_com_konglong87_niuma_internal_goal.GoalRisk"][];
            steps?: components["schemas"]["github_com_konglong87_niuma_internal_goal.GoalStep"][];
            summary?: string;
            updated_at?: string;
            version?: number;
        };
        "internal_server.SwaggerGoalResponse": {
            created_at?: string;
            cwd?: string;
            error?: string;
            id?: string;
            input_tokens?: number;
            last_blocker?: string;
            last_checkpoint?: string;
            last_next_action?: string;
            last_reason?: string;
            model?: string;
            objective?: string;
            output_tokens?: number;
            provider?: string;
            repeated_blocker_count?: number;
            session_id?: string;
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Status"];
            token_budget?: number;
            turn_budget?: number;
            turns_used?: number;
            updated_at?: string;
        };
        "internal_server.SwaggerHealthResponse": {
            ok?: boolean;
            workspace?: string;
        };
        "internal_server.SwaggerIDResponse": {
            id?: number;
        };
        "internal_server.SwaggerImageArtifactResponse": {
            asset?: components["schemas"]["github_com_konglong87_niuma_internal_imagegen.Artifact"];
        };
        "internal_server.SwaggerImageCapabilitiesResponse": {
            data?: components["schemas"]["internal_server.SwaggerImageCapabilityEntry"][];
        };
        "internal_server.SwaggerImageCapabilityEntry": {
            capability?: components["schemas"]["github_com_konglong87_niuma_internal_config.ImageModelCapability"];
            image_protocol?: string;
            label?: string;
            model?: string;
            provider?: string;
        };
        "internal_server.SwaggerImageGenerateRequest": {
            aspect_ratio?: string;
            background?: string;
            idempotency_key?: string;
            model?: string;
            output_format?: string;
            prompt?: string;
            provider?: string;
            quality?: string;
            resolution?: string;
            size?: string;
            watermark?: boolean;
        };
        "internal_server.SwaggerImageGenerationListResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_imagegen.GenerationRecord"][];
        };
        "internal_server.SwaggerKnowledgeSearchResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.KnowledgeChunk"][];
        };
        "internal_server.SwaggerListAgentSlashCommandsResponse": {
            data?: components["schemas"]["internal_server.SwaggerAgentSlashCommand"][];
        };
        "internal_server.SwaggerListAgentTaskEventsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent"][];
        };
        "internal_server.SwaggerListAgentTasksResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"][];
        };
        "internal_server.SwaggerListAuditLogsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AuditLog"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListDocumentsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Document"][];
        };
        "internal_server.SwaggerListEffectiveSkillsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.EffectiveSkill"][];
        };
        "internal_server.SwaggerListGoalEventsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Event"][];
        };
        "internal_server.SwaggerListGoalEvidenceResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_goal.GoalEvidence"][];
        };
        "internal_server.SwaggerListGoalsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Goal"][];
        };
        "internal_server.SwaggerListKnowledgeDocumentsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.KnowledgeDocument"][];
        };
        "internal_server.SwaggerListLocalSkillsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_skills.Skill"][];
            workspace?: string;
        };
        "internal_server.SwaggerListMemoriesResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Memory"][];
        };
        "internal_server.SwaggerListMessagesResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Message"][];
        };
        "internal_server.SwaggerListSessionsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Session"][];
        };
        "internal_server.SwaggerListSkillOverridesResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.SkillOverride"][];
        };
        "internal_server.SwaggerListSkillsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Skill"][];
        };
        "internal_server.SwaggerListTelemetryEventsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.TelemetryEvent"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListTenantQuotaEventsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.QuotaEvent"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListTenantsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Tenant"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListTenantUsageDailyResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.UsageDaily"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListTenantUsageLedgerResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.UsageLedger"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListUsersResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.User"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerListWebAgentConversationsResponse": {
            data?: components["schemas"]["internal_server.SwaggerWebAgentConversation"][];
        };
        "internal_server.SwaggerLivenessResponse": {
            /** @example ok */
            status?: string;
        };
        "internal_server.SwaggerMemoryReviewRequest": {
            action?: string;
            memory_key?: string;
        };
        "internal_server.SwaggerMobileAttachment": {
            attachment_id?: string;
            media_type?: string;
            name?: string;
            sha256?: string;
            size_bytes?: number;
            transcript?: string;
            type?: string;
            url?: string;
        };
        "internal_server.SwaggerMobileAttachmentPresignRequest": {
            media_type?: string;
            name?: string;
            sha256?: string;
            size_bytes?: number;
            type?: string;
        };
        "internal_server.SwaggerMobileAttachmentPresignResponse": {
            attachment?: components["schemas"]["internal_server.SwaggerMobileAttachment"];
            attachment_id?: string;
            expires_at?: string;
            headers?: {
                [key: string]: string;
            };
            max_size_bytes?: number;
            object_key?: string;
            upload_url?: string;
        };
        "internal_server.SwaggerMobileBranchRequest": {
            session_key?: string;
            title?: string;
            until_message_id?: number;
            until_turn?: number;
        };
        "internal_server.SwaggerMobileBranchResponse": {
            copied_messages?: number;
            id?: number;
            parent_session_id?: number;
            session_key?: string;
            until_turn?: number;
        };
        "internal_server.SwaggerMobileCancelResponse": {
            active?: boolean;
            id?: number;
            status?: string;
        };
        "internal_server.SwaggerMobileLatestRecap": {
            content?: string;
            created_at?: string;
            message_id?: number;
            model?: string;
            turn_index?: number;
        };
        "internal_server.SwaggerMobileMessageListResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Message"][];
            has_more?: boolean;
            next_cursor?: string;
        };
        "internal_server.SwaggerMobileMessageStreamRequest": {
            attachments?: components["schemas"]["internal_server.SwaggerMobileAttachment"][];
            content?: string;
            message_key?: string;
            model?: string;
        };
        "internal_server.SwaggerMobileRegenerateRequest": {
            message_key?: string;
            model?: string;
        };
        "internal_server.SwaggerMobileSessionDetailResponse": {
            cwd?: string;
            id?: number;
            last_message_at?: string;
            latest_recap?: components["schemas"]["internal_server.SwaggerMobileLatestRecap"];
            model?: string;
            session_key?: string;
            started_at?: string;
            status?: string;
            title?: string;
        };
        "internal_server.SwaggerMobileSessionRequest": {
            cwd?: string;
            metadata_json?: string;
            model?: string;
            session_key?: string;
            status?: string;
            title?: string;
        };
        "internal_server.SwaggerOpenAIChatResponse": {
            choices?: components["schemas"]["internal_server.SwaggerOpenAIChoice"][];
            created?: number;
            id?: string;
            model?: string;
            object?: string;
            tenant_runtime?: components["schemas"]["internal_server.SwaggerTenantRuntimeMetadata"];
            usage?: components["schemas"]["internal_server.SwaggerOpenAIUsage"];
        };
        "internal_server.SwaggerOpenAIChoice": {
            finish_reason?: string;
            index?: number;
            message?: components["schemas"]["internal_server.SwaggerOpenAIMessage"];
        };
        "internal_server.SwaggerOpenAIMessage": {
            content?: string;
            role?: string;
        };
        "internal_server.SwaggerOpenAIModel": {
            created?: number;
            id?: string;
            object?: string;
            owned_by?: string;
        };
        "internal_server.SwaggerOpenAIModelsResponse": {
            data?: components["schemas"]["internal_server.SwaggerOpenAIModel"][];
            object?: string;
        };
        "internal_server.SwaggerOpenAIUsage": {
            completion_tokens?: number;
            prompt_tokens?: number;
            total_tokens?: number;
        };
        "internal_server.SwaggerPendingInputRequest": {
            attachments?: components["schemas"]["internal_server.SwaggerAgentTaskAttachment"][];
            /** @example web-123 */
            client_input_id?: string;
            content?: string;
            direction?: string;
        };
        "internal_server.SwaggerPendingInputResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_pendinginput.PendingInput"];
            session_id?: number;
            source_pending_input_id?: string;
            task_id?: number;
        };
        "internal_server.SwaggerPendingInputSettingsRequest": {
            enabled?: boolean;
        };
        "internal_server.SwaggerPromptDumpFilter": {
            include_request?: boolean;
            limit?: number;
            session_id?: string;
        };
        "internal_server.SwaggerPromptDumpRecordsResponse": {
            exists?: boolean;
            filter?: components["schemas"]["internal_server.SwaggerPromptDumpFilter"];
            generated_at?: string;
            path?: string;
            records?: components["schemas"]["internal_server.SwaggerPromptDumpRecordSummary"][];
            sessions?: components["schemas"]["internal_server.SwaggerPromptDumpSessionSummary"][];
            size_bytes?: number;
            warning?: string;
        };
        "internal_server.SwaggerPromptDumpRecordSummary": {
            cache_control_blocks?: number;
            cache_control_bytes?: number;
            cache_diagnostics?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.CacheRecordDiagnostics"];
            context_manifest?: number[];
            line_number?: number;
            max_tokens?: number;
            message_count?: number;
            messages_summary?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.MessageSummary"][];
            model?: string;
            prompt_mode?: string;
            request?: number[];
            request_redaction?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.RequestRedaction"];
            scope?: string;
            session_id?: string;
            system_block_count?: number;
            system_blocks_summary?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.SystemBlockSummary"][];
            system_bytes?: number;
            system_hash?: string;
            timestamp?: string;
            tool_count?: number;
            tools_summary?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.ToolSummary"][];
            turn?: number;
        };
        "internal_server.SwaggerPromptDumpSessionSummary": {
            cache_control_blocks?: number;
            cache_diagnostics?: components["schemas"]["github_com_konglong87_niuma_internal_promptdump.CacheSessionDiagnostics"];
            first_timestamp?: string;
            last_timestamp?: string;
            max_turn?: number;
            models?: string[];
            prompt_modes?: string[];
            raw_request_records?: number;
            records?: number;
            scopes?: string[];
            session_id?: string;
            system_bytes_max?: number;
            system_hashes?: string[];
            tool_count_max?: number;
            trace_summary?: components["schemas"]["internal_server.traceSummary"];
            trace_usage_available?: boolean;
        };
        "internal_server.SwaggerPromptTemplate": {
            category?: string;
            content?: string;
            created_at?: string;
            id?: number;
            pinned?: boolean;
            sort_order?: number;
            tenant_id?: number;
            title?: string;
            updated_at?: string;
            user_id?: number;
        };
        "internal_server.SwaggerPromptTemplateRequest": {
            category?: string;
            content?: string;
            id?: number;
            pinned?: boolean;
            sort_order?: number;
            title?: string;
        };
        "internal_server.SwaggerProviderListResponse": {
            data?: components["schemas"]["internal_server.SwaggerProviderOption"][];
            object?: string;
        };
        "internal_server.SwaggerProviderOption": {
            model?: string;
            name?: string;
        };
        "internal_server.SwaggerQueryResult": {
            model?: string;
            response?: string;
            session_id?: string;
            stop_reason?: string;
            tool_calls?: components["schemas"]["github_com_konglong87_niuma_internal_query.ToolTrace"][];
            transcript_path?: string;
            turns?: number;
            usage?: components["schemas"]["github_com_konglong87_niuma_internal_query.Usage"];
        };
        "internal_server.SwaggerReadinessResponse": {
            /** @description Checks 是依赖名到 "ok"/"unavailable" 的映射，键取决于服务端配置了哪些依赖。 */
            checks?: {
                [key: string]: string;
            };
            /** @example ok */
            status?: string;
        };
        "internal_server.SwaggerRunGoalResponse": {
            decision?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Decision"];
            event?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Event"];
            goal?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Goal"];
        };
        "internal_server.SwaggerRuntimeBackgroundEventsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_scheduler.Event"][];
            next_offset?: number;
        };
        "internal_server.SwaggerRuntimeBackgroundListResponse": {
            data?: components["schemas"]["internal_server.RuntimeBackgroundJob"][];
        };
        "internal_server.SwaggerRuntimeBackgroundLogsResponse": {
            id?: string;
            log_tail?: string;
            logs?: string;
        };
        "internal_server.SwaggerRuntimeBackgroundRunsResponse": {
            data?: components["schemas"]["github_com_konglong87_niuma_internal_scheduler.RunRecord"][];
        };
        "internal_server.SwaggerRuntimeBackgroundStopResponse": {
            disabled?: boolean;
            id?: string;
            schedule_id?: string;
            stopped?: boolean;
        };
        "internal_server.SwaggerRuntimeLoopRequest": {
            cwd?: string;
            interval?: string;
            interval_seconds?: number;
            max_tokens?: number;
            max_turns?: number;
            model?: string;
            prompt?: string;
        };
        "internal_server.SwaggerSessionControlAttachment": {
            /** @example att_123 */
            attachment_id?: string;
            /** @example iVBORw0KGgo... */
            inline_data?: string;
            /** @example image/png */
            media_type?: string;
            /** @example screenshot.png */
            name?: string;
            /** @example abc123 */
            sha256?: string;
            /** @example 1024 */
            size_bytes?: number;
            /** @example image */
            type?: string;
            /** @example https://cdn.example.test/screenshot.png */
            url?: string;
        };
        "internal_server.SwaggerSessionControlAttachRequest": {
            /** @example handoff */
            relation_type?: string;
            /**
             * @example [
             *       "tenant:research",
             *       "local:review"
             *     ]
             */
            sources?: string[];
            /** @example 16384 */
            target_context_window_tokens?: number;
            /** @example 41 */
            target_task_id?: number;
        };
        "internal_server.SwaggerSessionControlCreateRequest": {
            /** @example /workspace/project */
            cwd?: string;
            /** @example high */
            effort?: string;
            /** @example Start by reviewing the rollout plan. */
            initial_text?: string;
            /** @example claude-sonnet-4-5 */
            model?: string;
            /** @example ask */
            permission_mode?: string;
            /** @enum {string} */
            prompt_mode?: "code" | "chat";
            provider?: string;
            /** @example support-case-42 */
            session_key?: string;
            /** @example Support case 42 */
            title?: string;
        };
        "internal_server.SwaggerSessionControlError": {
            /** @example invalid_state */
            code?: string;
            /** @example session is not available in this state */
            error?: string;
        };
        "internal_server.SwaggerSessionControlHandoff": {
            estimated_tokens?: number;
            replayed?: boolean;
            source_results?: components["schemas"]["internal_server.SwaggerSessionControlHandoffSource"][];
            stale?: boolean;
            target_task_id?: number;
        };
        "internal_server.SwaggerSessionControlHandoffSource": {
            candidate_removals?: components["schemas"]["github_com_konglong87_niuma_internal_sessioncontrol.HandoffCandidateRemoval"][];
            cursor_prefix?: string;
            error_code?: string;
            estimated_tokens?: number;
            event_id?: number;
            link_id?: number;
            package_id?: string;
            package_sha256_prefix?: string;
            source?: string;
            success?: boolean;
        };
        "internal_server.SwaggerSessionControlLink": {
            created_at?: string;
            id?: number;
            /** @example handoff */
            relation_type?: string;
            /** @example local:source */
            source?: string;
            /** @example tenant:target */
            target?: string;
        };
        "internal_server.SwaggerSessionControlMonitorRequest": {
            /**
             * @example feishu
             * @enum {string}
             */
            channel: "feishu";
            /** @example 300 */
            interval_seconds: number;
            /**
             * @example [
             *       "tenant:research"
             *     ]
             */
            sources: string[];
        };
        "internal_server.SwaggerSessionControlOperation": {
            audit_id?: number;
            error_code?: string;
            handoff?: components["schemas"]["internal_server.SwaggerSessionControlHandoff"];
            link_ids?: number[];
            operation_id?: string;
            replayed?: boolean;
            run_id?: number;
            schedule_id?: string;
            session?: components["schemas"]["internal_server.SwaggerSessionControlSession"];
        };
        "internal_server.SwaggerSessionControlOperationResponse": {
            data?: components["schemas"]["internal_server.SwaggerSessionControlOperation"];
        };
        "internal_server.SwaggerSessionControlSendRequest": {
            attachments?: components["schemas"]["internal_server.SwaggerSessionControlAttachment"][];
            /** @example Continue with the verified plan. */
            content?: string;
            /** @example high */
            effort?: string;
            /** @example claude-sonnet-4-5 */
            model?: string;
            /** @example ask */
            permission_mode?: string;
            /** @enum {string} */
            prompt_mode?: "code" | "chat";
            provider?: string;
            /**
             * @example [
             *       "tenant:research"
             *     ]
             */
            source_refs?: string[];
        };
        "internal_server.SwaggerSessionControlSession": {
            active_run_id?: number;
            cwd?: string;
            effort?: string;
            id?: number;
            links?: components["schemas"]["internal_server.SwaggerSessionControlLink"][];
            model?: string;
            permission_mode?: string;
            /** @enum {string} */
            prompt_mode?: "code" | "chat";
            provider?: string;
            read_only?: boolean;
            /** @example tenant:support-case-42 */
            ref?: string;
            /** @example support-case */
            short_id?: string;
            /**
             * @example tenant
             * @enum {unknown}
             */
            source?: "tenant" | "local";
            /**
             * @example running
             * @enum {unknown}
             */
            status?: "idle" | "queued" | "running" | "waiting_permission" | "waiting_input" | "blocked" | "completed" | "failed" | "stopped" | "archived";
            title?: string;
            updated_at?: string;
        };
        "internal_server.SwaggerSessionControlSessionListResponse": {
            data?: components["schemas"]["internal_server.SwaggerSessionControlSession"][];
        };
        "internal_server.SwaggerSessionControlSessionResponse": {
            data?: components["schemas"]["internal_server.SwaggerSessionControlSession"];
        };
        "internal_server.SwaggerSessionControlStateEvent": {
            /** @example 81 */
            cursor?: string;
            operation_id?: string;
            run_id?: number;
            /** @example niuma.session-control-state.v1 */
            schema_version?: string;
            /** @example tenant:support-case-42 */
            session_ref?: string;
            /** @example running */
            status?: components["schemas"]["github_com_konglong87_niuma_internal_sessioncontrol.SessionStatus"];
            updated_at?: string;
        };
        "internal_server.SwaggerSessionControlStopRequest": Record<string, never>;
        "internal_server.SwaggerSessionConversationResponse": {
            data?: components["schemas"]["internal_server.sessionConversationPage"];
        };
        "internal_server.SwaggerSessionConversationSubscribe": {
            sessions?: components["schemas"]["internal_server.conversationSubscription"][];
        };
        "internal_server.SwaggerSessionIDResponse": {
            id?: number;
            session_key?: string;
        };
        "internal_server.SwaggerSkillPackageVerifyRequest": {
            expected_package_sha256?: string;
            expected_version?: number;
            model?: string;
            prompt?: string;
            schema_name?: string;
            skill_key?: string;
        };
        "internal_server.SwaggerSkillPackageVerifyResponse": {
            error?: string;
            ok?: boolean;
            tenant_runtime?: components["schemas"]["internal_server.openAITenantRuntimeMetadata"];
            trace_id?: string;
        };
        "internal_server.SwaggerSkillRollbackRequest": {
            skill_key?: string;
            target_version?: number;
            version?: number;
        };
        "internal_server.SwaggerSkillRollbackResponse": {
            from_version?: number;
            id?: number;
            skill_key?: string;
            version?: number;
        };
        "internal_server.SwaggerSnapshotResponse": {
            data?: {
                [key: string]: unknown;
            };
        };
        "internal_server.SwaggerSSEEvent": {
            delta?: string;
            error?: string;
            message_id?: number;
            message_key?: string;
            session_id?: number;
            status?: string;
            type?: string;
        };
        "internal_server.SwaggerTelemetryEventRequest": {
            category?: string;
            duration_ms?: number;
            error?: string;
            input_tokens?: number;
            model?: string;
            name?: string;
            output_tokens?: number;
            parent_span_id?: string;
            properties?: {
                [key: string]: unknown;
            };
            resource_id?: string;
            resource_type?: string;
            schema_version?: string;
            session_id?: number;
            source?: string;
            span_id?: string;
            started_at?: string;
            status?: string;
            tool_name?: string;
            turn_index?: number;
        };
        "internal_server.SwaggerTenantContext": {
            tenant?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Tenant"];
            tenant_id?: number;
            user_id?: number;
            user_key?: string;
        };
        "internal_server.SwaggerTenantDocumentRequest": {
            active?: boolean;
            content_json?: string;
            content_md?: string;
            doc_type?: string;
            title?: string;
            version?: number;
        };
        "internal_server.SwaggerTenantKnowledgeDocumentRequest": {
            content?: string;
            metadata_json?: string;
            source_type?: string;
            status?: string;
            title?: string;
        };
        "internal_server.SwaggerTenantKnowledgeSearchRequest": {
            limit?: number;
            query?: string;
        };
        "internal_server.SwaggerTenantMemoryRequest": {
            category?: string;
            content?: string;
            embedding_ref?: string;
            importance?: number;
            memory_key?: string;
            metadata_json?: string;
            source?: string;
        };
        "internal_server.SwaggerTenantMessageRequest": {
            content?: string;
            content_json?: string;
            input_tokens?: number;
            is_error?: boolean;
            model?: string;
            output_tokens?: number;
            role?: string;
            session_id?: number;
            tool_id?: string;
            tool_name?: string;
            trace_id?: string;
            turn_index?: number;
        };
        "internal_server.SwaggerTenantProfile": {
            created_at?: string;
            generated_from_session_id?: number;
            id?: number;
            profile_json?: string;
            profile_version?: number;
            summary?: string;
        };
        "internal_server.SwaggerTenantProfileRequest": {
            generated_from_session_id?: number;
            profile_json?: string;
            profile_version?: number;
            summary?: string;
        };
        "internal_server.SwaggerTenantQuotaConfig": {
            created_at?: string;
            daily_message_limit?: number;
            daily_token_limit?: number;
            max_concurrent_requests?: number;
            qps_limit?: number;
            quota_enabled?: boolean;
            reserve_output_tokens?: number;
            status?: string;
            tenant_id?: number;
            timezone?: string;
            updated_at?: string;
            updated_by_user_id?: number;
        };
        "internal_server.SwaggerTenantQuotaConfigRequest": {
            daily_message_limit?: number;
            daily_token_limit?: number;
            max_concurrent_requests?: number;
            qps_limit?: number;
            quota_enabled?: boolean;
            reserve_output_tokens?: number;
            status?: string;
            timezone?: string;
        };
        "internal_server.SwaggerTenantRequest": {
            name?: string;
            settings_json?: string;
            status?: string;
            tenant_key?: string;
        };
        "internal_server.SwaggerTenantRuntimeMetadata": {
            active?: boolean;
            bytes?: number;
            loaded_keys?: string[];
            package_refs?: string[];
            package_sha256?: string[];
            resolved?: boolean;
            runtime_refs?: string[];
            skill_keys?: string[];
            source?: string;
            tenant_addendum?: boolean;
            versions?: string[];
        };
        "internal_server.SwaggerTenantSession": {
            cwd?: string;
            id?: number;
            last_message_at?: string;
            model?: string;
            session_key?: string;
            started_at?: string;
            status?: string;
            title?: string;
        };
        "internal_server.SwaggerTenantSessionRequest": {
            cwd?: string;
            metadata_json?: string;
            model?: string;
            session_key?: string;
            status?: string;
            title?: string;
        };
        "internal_server.SwaggerTenantSessionTimelineResponse": {
            agent_task_events?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent"][];
            agent_tasks?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"][];
            audit_logs?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AuditLog"][];
            messages?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Message"][];
            session?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Session"];
            telemetry_events?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.TelemetryEvent"][];
            timeline?: components["schemas"]["internal_server.SwaggerTenantTimelineItem"][];
            trace_ids?: string[];
        };
        "internal_server.SwaggerTenantSkillOverrideRequest": {
            config_json?: string;
            enabled?: boolean;
            skill_key?: string;
            version?: number;
        };
        "internal_server.SwaggerTenantSkillPackageRequest": {
            content_base64?: string;
            description?: string;
            enabled?: boolean;
            name?: string;
            skill_key?: string;
            source_path?: string;
            store_root?: string;
            version?: number;
        };
        "internal_server.SwaggerTenantSkillPackageResult": {
            description?: string;
            enabled?: boolean;
            id?: number;
            manifest?: components["schemas"]["github_com_konglong87_niuma_internal_tenantpkg.Manifest"];
            manifest_ref?: string;
            name?: string;
            package_ref?: string;
            package_sha256?: string;
            runtime_md?: string;
            runtime_ref?: string;
            skill_key?: string;
            version?: number;
        };
        "internal_server.SwaggerTenantSkillRequest": {
            config_json?: string;
            content_md?: string;
            description?: string;
            enabled?: boolean;
            manifest_json?: string;
            name?: string;
            package_ref?: string;
            package_sha256?: string;
            runtime_ref?: string;
            skill_key?: string;
            version?: number;
        };
        "internal_server.SwaggerTenantTimelineItem": {
            agent_task?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"];
            agent_task_event?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent"];
            audit_log?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AuditLog"];
            message?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Message"];
            telemetry_event?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.TelemetryEvent"];
            time?: string;
            trace_id?: string;
            type?: string;
        };
        "internal_server.SwaggerTenantUser": {
            display_name?: string;
            email?: string;
            id?: number;
            metadata_json?: string;
            role?: string;
            status?: string;
            tenant_id?: number;
            updated_at?: string;
            user_info_json?: string;
            user_key?: string;
        };
        "internal_server.SwaggerTenantUserRequest": {
            display_name?: string;
            email?: string;
            metadata_json?: string;
            role?: string;
            status?: string;
            user_info_json?: string;
            user_key?: string;
        };
        "internal_server.SwaggerTraceDetailResponse": {
            diagnostics?: components["schemas"]["internal_server.traceDiagnostic"][];
            events?: components["schemas"]["internal_server.traceEvent"][];
            generated_at?: string;
            latest_recap?: components["schemas"]["internal_server.traceRecap"];
            quality?: components["schemas"]["internal_server.traceQuality"];
            raw?: unknown;
            recaps?: components["schemas"]["internal_server.traceRecap"][];
            rewind?: components["schemas"]["internal_server.traceRewind"];
            session_id?: string;
            source?: string;
            span_tree?: components["schemas"]["internal_server.traceTree"][];
            spans?: components["schemas"]["internal_server.traceSpan"][];
            summary?: components["schemas"]["internal_server.traceSummary"];
            title?: string;
            trace_ids?: string[];
        };
        "internal_server.SwaggerTraceSessionsResponse": {
            data?: components["schemas"]["internal_server.traceSessionSummary"][];
            source?: string;
        };
        "internal_server.SwaggerUpdateAgentTaskRequest": {
            metadata_json?: number[];
            result_json?: number[];
            /** @example completed */
            status?: string;
        };
        "internal_server.SwaggerUpdateAgentTaskResponse": {
            id?: number;
            updated?: boolean;
        };
        "internal_server.SwaggerUpdateGoalRequest": {
            cwd?: string;
            error?: string;
            input_tokens?: number;
            last_blocker?: string;
            last_checkpoint?: string;
            last_next_action?: string;
            last_reason?: string;
            model?: string;
            objective?: string;
            output_tokens?: number;
            repeated_blocker_count?: number;
            session_id?: string;
            /** @example blocked */
            status?: components["schemas"]["github_com_konglong87_niuma_internal_goal.Status"];
            token_budget?: number;
            turn_budget?: number;
            turns_used?: number;
        };
        "internal_server.SwaggerWebAgentConversation": {
            cwd?: string;
            id?: string;
            latest_task?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"];
            session?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Session"];
            session_id?: number;
            session_key?: string;
            status?: string;
            tasks?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"][];
            title?: string;
            updated_at?: string;
            workspace_name?: string;
        };
        "internal_server.SwaggerWebAgentConversationDetail": {
            cwd?: string;
            events?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent"][];
            id?: string;
            latest_task?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"];
            messages?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Message"][];
            session?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.Session"];
            session_id?: number;
            session_key?: string;
            status?: string;
            tasks?: components["schemas"]["github_com_konglong87_niuma_internal_storage_mysql.AgentTask"][];
            title?: string;
            updated_at?: string;
            usage?: components["schemas"]["internal_server.SwaggerWebAgentConversationUsage"];
            workspace_name?: string;
        };
        "internal_server.SwaggerWebAgentConversationUsage": {
            cancelled_runs?: number;
            completed_runs?: number;
            context_length?: number;
            context_percent?: number;
            failed_runs?: number;
            input_tokens?: number;
            output_tokens?: number;
            running_runs?: number;
            timeout_runs?: number;
            tool_calls?: number;
            total_duration_ms?: number;
            total_runs?: number;
            total_tokens?: number;
        };
        "internal_server.SwaggerWorkflowBuiltins": {
            items?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.Builtin"][];
        };
        "internal_server.SwaggerWorkflowEvents": {
            events?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Event"][];
            has_more?: boolean;
            last_sequence?: number;
        };
        "internal_server.SwaggerWorkflowRecovery": {
            revision?: number;
        };
        "internal_server.SwaggerWorkflowRun": {
            replayed?: boolean;
            run?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Run"];
        };
        "internal_server.SwaggerWorkflowRuns": {
            items?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflow.Run"][];
        };
        "internal_server.SwaggerWorkflowTemplate": {
            template?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.Template"];
        };
        "internal_server.SwaggerWorkflowTemplates": {
            items?: components["schemas"]["github_com_konglong87_niuma_internal_agentteam_workflowapp.Template"][];
        };
        "internal_server.SwaggerWorkflowTypes": {
            execution_kinds?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.ExecutionKindDescriptor"][];
            roles?: components["schemas"]["github_com_konglong87_niuma_internal_agentprofile.RoleTypeDescriptor"][];
        };
        "internal_server.traceDiagnostic": {
            code?: string;
            count?: number;
            estimated_savings_ms?: number;
            fingerprint?: string;
            message?: string;
            severity?: string;
            span_ids?: string[];
            tool_name?: string;
            turn_index?: number;
        };
        "internal_server.traceEvent": {
            content?: string;
            duration_ms?: number;
            id?: string;
            input?: string;
            is_error?: boolean;
            model?: string;
            name?: string;
            output?: string;
            parent_span_id?: string;
            properties?: {
                [key: string]: unknown;
            };
            raw?: unknown;
            role?: string;
            skill?: string;
            span_id?: string;
            started_at?: string;
            status?: string;
            time?: string;
            tool_id?: string;
            tool_name?: string;
            trace_id?: string;
            turn_index?: number;
            type?: string;
        };
        "internal_server.traceQuality": {
            completion_verified?: boolean;
            failed_test_attempts?: number;
            final_verification_passed?: boolean;
            gate_blocks?: number;
            recoveries?: number;
            test_attempts?: number;
            test_failure_recovered?: boolean;
            tests_passed?: boolean;
            tests_run?: boolean;
            todo_writes?: number;
            tool_errors?: number;
        };
        "internal_server.traceRecap": {
            content?: string;
            duration_ms?: number;
            id?: string;
            model?: string;
            properties?: {
                [key: string]: unknown;
            };
            source?: string;
            status?: string;
            summarizes_entry_id?: string;
            time?: string;
        };
        "internal_server.traceRewind": {
            checkpoints?: components["schemas"]["internal_server.traceRewindCheckpoint"][];
            rewinds?: components["schemas"]["internal_server.traceRewindEvent"][];
        };
        "internal_server.traceRewindCheckpoint": {
            events_after?: number;
            file_changes?: number;
            id?: string;
            index?: number;
            message?: string;
            message_id?: string;
            name?: string;
            next_index?: number;
            time?: string;
        };
        "internal_server.traceRewindEvent": {
            content?: string;
            id?: string;
            index?: number;
            name?: string;
            time?: string;
        };
        "internal_server.traceSessionSummary": {
            cwd?: string;
            message_hint?: string;
            model?: string;
            path?: string;
            session_id?: string;
            source?: string;
            started_at?: string;
            status?: string;
            title?: string;
            updated_at?: string;
        };
        "internal_server.traceSpan": {
            concurrency_class?: string;
            depth?: number;
            duration_ms?: number;
            end?: string;
            error?: string;
            id?: string;
            model?: string;
            name?: string;
            parent_id?: string;
            self_duration_ms?: number;
            sequence?: number;
            skill?: string;
            start?: string;
            status?: string;
            tool_name?: string;
            trace_id?: string;
            turn_index?: number;
            type?: string;
        };
        "internal_server.traceSummary": {
            agent_tasks?: number;
            cache_creation_tokens?: number;
            cache_hit_ratio?: number;
            cache_read_tokens?: number;
            cache_summary?: string;
            critical_path_ms?: number;
            duration_ms?: number;
            errors?: number;
            files_changed?: number;
            input_tokens?: number;
            messages?: number;
            model_wall_ms?: number;
            output_tokens?: number;
            parallel_savings_ms?: number;
            permission_decisions?: number;
            skills?: string[];
            task_wall_ms?: number;
            token_summary?: string;
            tool_calls?: number;
            tool_errors?: number;
            tool_wall_ms?: number;
            tool_work_ms?: number;
            total_model_duration_ms?: number;
            total_tokens?: number;
            total_tool_duration_ms?: number;
            turns?: number;
            unattributed_ms?: number;
        };
        "internal_server.traceTree": {
            children?: components["schemas"]["internal_server.traceTree"][];
            span?: components["schemas"]["internal_server.traceSpan"];
        };
    };
    responses: never;
    parameters: never;
    requestBodies: {
        /** @description Team bindings */
        "internal_server.SwaggerAgentTeamBindingArray": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerAgentTeamBinding"][];
            };
        };
        /** @description Team members */
        "internal_server.SwaggerAgentTeamMemberArray": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerAgentTeamMember"][];
            };
        };
        /** @description Mobile session request */
        "internal_server.SwaggerMobileSessionRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerMobileSessionRequest"];
            };
        };
        /** @description Pending input */
        "internal_server.SwaggerPendingInputRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerPendingInputRequest"];
            };
        };
        /** @description Queue setting */
        "internal_server.SwaggerPendingInputSettingsRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerPendingInputSettingsRequest"];
            };
        };
        /** @description Loop request */
        "internal_server.SwaggerRuntimeLoopRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerRuntimeLoopRequest"];
            };
        };
        /** @description Optional empty JSON object */
        "internal_server.SwaggerSessionControlStopRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerSessionControlStopRequest"];
            };
        };
        /** @description Tenant request */
        "internal_server.SwaggerTenantRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerTenantRequest"];
            };
        };
        /** @description Session request */
        "internal_server.SwaggerTenantSessionRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerTenantSessionRequest"];
            };
        };
        /** @description Skill package request */
        "internal_server.SwaggerTenantSkillPackageRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerTenantSkillPackageRequest"];
            };
        };
        /** @description User request */
        "internal_server.SwaggerTenantUserRequest": {
            content: {
                "application/json": components["schemas"]["internal_server.SwaggerTenantUserRequest"];
            };
        };
        /** @description Full global settings document */
        Request: {
            content: {
                "application/json": Record<string, never>;
            };
        };
        /** @description Assignment */
        Request2: {
            content: {
                "application/json": {
                    [key: string]: unknown;
                };
            };
        };
        /** @description Bot binding */
        Request3: {
            content: {
                "application/json": {
                    [key: string]: unknown;
                };
            };
        };
        /** @description Profile draft */
        Request4: {
            content: {
                "application/json": {
                    [key: string]: unknown;
                };
            };
        };
        /** @description Existing resource request body */
        Request5: {
            content: {
                "application/json": Record<string, never>;
            };
        };
        /** @description Team draft */
        Request6: {
            content: {
                "application/json": {
                    [key: string]: unknown;
                };
            };
        };
    };
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export type operations = Record<string, never>;
