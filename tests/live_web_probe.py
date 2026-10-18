"""Compare unchanged WhiteDNS engine modes using complete HTTPS bodies.

Uses an isolated, temporary account exported by the panel. No SSH secrets or
server private keys are accepted. The profile and generated engine config contain
account credentials: keep the output directory private. Network tests are opt-in.
Use --mode exported to exercise the exact panel export without overrides.
Result reports omit zone and public key.
"""
import argparse,base64,concurrent.futures,hashlib,http.client,json,socket,ssl,subprocess,time
from pathlib import Path
from urllib.parse import urlsplit

URLS={
 'small':'https://example.com/',
 'ip':'https://api.ipify.org/',
 'large':'https://raw.githubusercontent.com/getlantern/soar/v0.2.2/client.go',
 'asset':'https://raw.githubusercontent.com/getlantern/soar/v0.2.2/data/resolvers/ir.txt',
}

def https(url,port=None):
 target=urlsplit(url)
 if port is None:
  connection=http.client.HTTPSConnection(target.hostname,443,timeout=40,context=ssl.create_default_context())
  connection.request('GET',target.path or '/',headers={'Connection':'close','Accept-Encoding':'identity'})
  response=connection.getresponse();data=response.read();status=response.status;connection.close();return status,data
 raw=socket.create_connection(('127.0.0.1',port),timeout=40)
 try:
  def exact(n):
   b=b''
   while len(b)<n:
    part=raw.recv(n-len(b))
    if not part:raise OSError('SOCKS negotiation closed')
    b+=part
   return b
  raw.sendall(b'\x05\x01\x00');assert exact(2)==b'\x05\x00'
  host=target.hostname.encode('ascii');raw.sendall(b'\x05\x01\x00\x03'+bytes([len(host)])+host+(443).to_bytes(2,'big'))
  reply=exact(10)
  if reply[1]:raise OSError('SOCKS CONNECT rejected: '+str(reply[1]))
  with ssl.create_default_context().wrap_socket(raw,server_hostname=target.hostname) as secure:
   request=f'GET {target.path or "/"} HTTP/1.1\r\nHost: {target.hostname}\r\nConnection: close\r\nAccept-Encoding: identity\r\n\r\n'
   secure.sendall(request.encode());response=http.client.HTTPResponse(secure);response.begin();data=response.read();return response.status,data
 finally:raw.close()

def main():
 parser=argparse.ArgumentParser();parser.add_argument('--profile',type=Path,required=True);parser.add_argument('--mode',choices=['balanced','conservative','bootstrap','stable','exported'],default='exported');parser.add_argument('--rounds',type=int,default=2);parser.add_argument('--engine',type=Path,required=True);parser.add_argument('--expected-ip',required=True);parser.add_argument('--workers',type=int,default=8);parser.add_argument('--output',type=Path,required=True);args=parser.parse_args()
 if not 1<=args.workers<=128 or not 1<=args.rounds<=20:parser.error('workers must be 1..128; rounds 1..20')
 record=json.loads(args.profile.read_text(encoding='utf8'));encoded=record['link'][7:];profile=json.loads(base64.urlsafe_b64decode(encoded+'='*((-len(encoded))%4)))['profile']
 out=args.output;out.mkdir(parents=True,exist_ok=True)
 baseline={}
 for label in ['large','asset']:
  status,data=https(URLS[label]);assert status==200;baseline[label]=hashlib.sha256(data).hexdigest();print(json.dumps({'baseline':label,'bytes':len(data)}),flush=True)
 with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
 settings=dict(profile['soarSettings'])
 if args.mode!='exported':
  settings.update(discovery=args.mode!='bootstrap',discoveryWant=8,discoveryConcurrency=4,discoveryPaceMs=500,maxInflight=128,maxPathWindow=32,maxAnswer=1232,maxQName=253,maxStreams=64)
 if args.mode=='conservative':settings.update(maxInflight=32,maxPathWindow=8,maxAnswer=512,maxQName=101,maxStreams=16)
 if args.mode=='stable':settings.update(maxInflight=32,maxPathWindow=8,maxAnswer=512,maxQName=101,maxStreams=128,discoveryWant=1,discoveryConcurrency=1,discoveryPaceMs=1500)
 config={'zone':profile['server']['domain'],'server_public_key':profile['server']['public_key'],'listen':f'127.0.0.1:{port}','soar':settings}
 (out/'config.json').write_text(json.dumps(config));(out/'resolvers.txt').write_text('\n'.join(profile['resolvers'])+'\n')
 results=[];process=None
 def request(label,tag):
  started=time.monotonic();result={'request':tag,'target':label}
  try:
   status,data=https(URLS[label],port);valid=status==200
   if label in baseline:valid=valid and hashlib.sha256(data).hexdigest()==baseline[label]
   if label=='ip':valid=valid and data.strip()==args.expected_ip.encode('ascii')
   result.update(ok=valid,status=status,bytes=len(data))
  except Exception as e:result.update(ok=False,error=str(e))
  result['seconds']=round(time.monotonic()-started,3);results.append(result);print(json.dumps(result),flush=True);return result
 try:
  with (out/'engine.log').open('w') as log:
   process=subprocess.Popen([str(args.engine.resolve()),'-config',str(out/'config.json'),'-resolvers',str(out/'resolvers.txt')],stdout=log,stderr=subprocess.STDOUT)
   start=time.monotonic()
   while time.monotonic()-start<100:
    if 'SOAR_READY authenticated=1 https=1' in (out/'engine.log').read_text():break
    if process.poll() is not None:raise RuntimeError('engine exited before readiness')
    time.sleep(.2)
   else:raise RuntimeError('startup deadline')
   print(json.dumps({'ready':True,'mode':args.mode,'seconds':round(time.monotonic()-start,3)}),flush=True)
   request('large','serial-large');request('small','serial-small');request('ip','exit-ip')
   for n in range(args.rounds):
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:list(pool.map(lambda i:request('asset',f'parallel-{n}-{i}'),range(args.workers)))
   request('large','final-large')
 finally:
  if process is not None:process.terminate();process.wait(timeout=5)
  (out/'result.json').write_text(json.dumps({'mode':args.mode,'results':results},indent=2))
 passed=bool(results) and all(r['ok'] for r in results);print(json.dumps({'passed':passed,'requests':len(results),'failed':sum(not r['ok'] for r in results)}),flush=True)
 raise SystemExit(0 if passed else 1)

if __name__=='__main__':main()
