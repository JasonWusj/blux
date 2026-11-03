export class ApiError extends Error {
	constructor(
		message: string,
		public code: string,
		public status = 0
	) {
		super(message);
		this.name = 'ApiError';
	}
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
	const headers = new Headers(init.headers);
	if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
	const timeout = AbortSignal.timeout(12_000);
	const signal = init.signal ? AbortSignal.any([init.signal, timeout]) : timeout;
	let response: Response;
	try {
		response = await fetch(`/api/v1${path}`, { ...init, headers, signal });
	} catch (error) {
		if (init.signal?.aborted) throw error;
		throw new ApiError(
			'暂时无法连接本地服务，请检查 Blux 是否正在运行，然后重试。',
			'connection_failed'
		);
	}
	const body = await response.json().catch(() => null);
	if (!response.ok) {
		throw new ApiError(
			body?.error?.message || `请求失败（${response.status}），请重试。`,
			body?.error?.code || 'request_failed',
			response.status
		);
	}
	if (body === null) {
		throw new ApiError('本地服务返回了无法读取的响应，请重试。', 'invalid_response');
	}
	return body as T;
}

export function errorText(error: unknown): string {
	return error instanceof Error ? error.message : '请求未完成，请重试。';
}
