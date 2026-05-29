const {
  askLlama,
  completeModeration,
  stopLlamaServer,
  aiConfigured,
  mergeChatCompletionBody,
  assistantTextFromChatResponse,
  startLlamaServer
} = require('./ai');

jest.mock('fs', () => ({
  existsSync: jest.fn(() => true),
  statSync: jest.fn(() => ({ size: 5000 })),
  readdirSync: jest.fn(() => []),
}));

jest.mock('child_process', () => {
  const mockSpawn = jest.fn(() => ({
    on: jest.fn(),
    kill: jest.fn(),
    stderr: { on: jest.fn() },
  }));
  return {
    spawn: mockSpawn,
  };
});

jest.mock('http', () => {
  const mockGet = jest.fn((url, cb) => {
    cb({ statusCode: 200, resume: () => {} });
    return { on: jest.fn() };
  });
  return {
    get: mockGet,
  };
});

jest.mock('os', () => ({
  cpus: jest.fn(() => []),
}));

jest.mock('path', () => ({
  join: jest.fn(),
  dirname: jest.fn(),
}));

describe('AI Module', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers();
  });

  afterEach(() => {
    stopLlamaServer();
    jest.useRealTimers();
  });

  it('mergeChatCompletionBody adds correct defaults', () => {
    const body = { messages: [{ role: 'user', content: 'hi' }], max_tokens: 10 };
    const result = mergeChatCompletionBody(body);
    expect(result.stream).toBe(false);
    expect(result.chat_template_kwargs).toEqual({ enable_thinking: false });
    expect(result.max_tokens).toBe(10);
  });

  it('startLlamaServer spawns process and checks health', async () => {
    const mockProc = { on: jest.fn(), kill: jest.fn(), stderr: { on: jest.fn() } };
    const childProcessMock = jest.requireMock('child_process');
    childProcessMock.spawn.mockReturnValue(mockProc);

    const httpMock = jest.requireMock('http');
    httpMock.get.mockImplementation((url, cb) => {
      cb({ statusCode: 200, resume: () => {} });
      return { on: jest.fn() };
    });

    const startPromise = startLlamaServer();

    // Advance the setInterval(1000ms) so the health check fires
    jest.advanceTimersByTime(1000);

    await startPromise;
    expect(childProcessMock.spawn).toHaveBeenCalled();
  });

  it('stopLlamaServer kills the process', async () => {
    const mockProc = { on: jest.fn(), kill: jest.fn(), stderr: { on: jest.fn() } };
    const childProcessMock = jest.requireMock('child_process');
    childProcessMock.spawn.mockReturnValue(mockProc);

    const httpMock = jest.requireMock('http');
    httpMock.get.mockImplementation((url, cb) => {
      cb({ statusCode: 200, resume: () => {} });
      return { on: jest.fn() };
    });

    const startPromise = startLlamaServer();

    // Advance the setInterval(1000ms) so the health check fires
    jest.advanceTimersByTime(1000);

    await startPromise;
    stopLlamaServer();

    expect(mockProc.kill).toHaveBeenCalled();
  });
});
