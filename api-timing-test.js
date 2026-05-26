// API 响应时间测试脚本
// 需要设置环境变量 ANTHROPIC_API_KEY

const https = require('https');

const API_KEY = process.env.ANTHROPIC_API_KEY || 'your-api-key-here';
const MODEL = 'claude-sonnet-4-6';

function testResponseTime() {
  const startTime = Date.now();

  const data = JSON.stringify({
    model: MODEL,
    max_tokens: 1024,
    messages: [{
      role: 'user',
      content: '你好'
    }]
  });

  const options = {
    hostname: 'api.anthropic.com',
    port: 443,
    path: '/v1/messages',
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'x-api-key': API_KEY,
      'anthropic-version': '2023-06-01',
      'Content-Length': data.length
    }
  };

  console.log('发送请求时间:', new Date().toISOString());
  console.log('开始时间戳:', startTime);
  console.log('-'.repeat(50));

  const req = https.request(options, (res) => {
    let responseData = '';
    const firstByteTime = Date.now();

    console.log('首字节时间 (TTFB):', firstByteTime - startTime, 'ms');

    res.on('data', (chunk) => {
      responseData += chunk;
    });

    res.on('end', () => {
      const endTime = Date.now();
      const totalTime = endTime - startTime;

      console.log('-'.repeat(50));
      console.log('完成时间:', new Date().toISOString());
      console.log('总响应时间:', totalTime, 'ms');
      console.log('首字节延迟 (TTFB):', firstByteTime - startTime, 'ms');
      console.log('传输时间:', endTime - firstByteTime, 'ms');

      try {
        const result = JSON.parse(responseData);
        if (result.content && result.content[0]) {
          console.log('\n回复内容:', result.content[0].text);
        }
      } catch (e) {
        console.log('响应:', responseData.substring(0, 200));
      }
    });
  });

  req.on('error', (e) => {
    console.error('请求错误:', e.message);
  });

  req.write(data);
  req.end();
}

testResponseTime();
