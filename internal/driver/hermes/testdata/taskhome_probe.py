import http.server, json, os, pathlib, re, sqlite3, subprocess, threading, time
home=pathlib.Path('/home/task'); hh=home/'hermes'; hh.mkdir(exist_ok=True); (home/'workspace').mkdir(exist_ok=True)
phase=os.environ.get('PROBE_PHASE','first')
start=time.monotonic(); requests=[]
class Provider(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a): pass
 def do_POST(self):
  body=json.loads(self.rfile.read(int(self.headers['Content-Length']))); requests.append(body)
  if self.path.endswith('/chat/completions') and self.headers.get('Authorization')!='Bearer fake-local-only':
   self.send_response(401);self.end_headers();return
  messages=body.get('messages',[])
  history=any('FIRST_STORED_MARKER' in str(m.get('content','')) for m in messages if m.get('role')=='assistant')
  answer='HISTORY_PRESENT' if history else 'FIRST_STORED_MARKER'
  summary=phase in ('compression','compressed-resume') and self.path.endswith('/chat/completions') and not body.get('tools')
  carry=any('COMPRESSION-CARRY-MARKER' in str(m.get('content','')) for m in messages)
  if summary: answer='COMPRESSION-CARRY-MARKER: FIRST_STORED_MARKER was learned in this conversation.'
  elif phase in ('compression','compressed-resume'): answer='COMPRESSED_HISTORY_PRESENT' if carry else 'COMPRESSION_NOT_SEEN'
  native=any('EXERCISE_NATIVE_TERMINAL' in str(m.get('content','')) for m in messages if m.get('role')=='user')
  tool=None
  if native and messages[-1].get('role')!='tool':
   tool=next((t['function']['name'] for t in body.get('tools',[]) if t.get('function',{}).get('name')=='terminal'),None)
  msg={'role':'assistant','content':answer}; finish='stop'
  if tool:
   msg={'role':'assistant','content':None,'tool_calls':[{'id':'call394','type':'function','function':{'name':tool,'arguments':json.dumps({'command':'printf terminal-proof > /home/task/workspace/terminal-proof','timeout':10})}}]}; finish='tool_calls'
  data={'id':'fake-394','object':'chat.completion','created':0,'model':'fake-model','choices':[{'index':0,'message':{'role':'assistant','content':answer},'finish_reason':'stop'}],'usage':{'prompt_tokens':10,'completion_tokens':5,'total_tokens':15}}
  data['choices'][0]['message']=msg; data['choices'][0]['finish_reason']=finish
  if body.get('stream'):
   delta=dict(msg)
   if tool: delta['tool_calls'][0]['index']=0
   chunks=[dict(data,object='chat.completion.chunk',choices=[{'index':0,'delta':delta,'finish_reason':None}]),dict(data,object='chat.completion.chunk',choices=[{'index':0,'delta':{},'finish_reason':finish}])]
   raw=(''.join('data: '+json.dumps(c)+'\n\n' for c in chunks)+'data: [DONE]\n\n').encode(); ct='text/event-stream'
  else: raw=json.dumps(data).encode(); ct='application/json'
  self.send_response(200); self.send_header('Content-Type',ct);self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
  print(json.dumps({'event':'request','time':time.time(),'path':self.path,'elapsed':round(time.monotonic()-start,3),'history':history,'stream':body.get('stream'),'native_tool':tool,'compression_summary':summary,'carried_summary':carry}),flush=True)
server=http.server.HTTPServer(('127.0.0.1',8765),Provider); threading.Thread(target=server.serve_forever,daemon=True).start()
(hh/'config.yaml').write_text('model:\n  default: fake-model\n  provider: probe\n  context_length: 128000\ncustom_providers:\n  - name: probe\n    base_url: http://127.0.0.1:8765/v1\n    key_env: PROBE_MODEL_KEY\ncompression:\n  threshold: 0.5\n  protect_first_n: 0\n  protect_last_n: 2\n  abort_on_summary_failure: true\napprovals:\n  mode: off\nterminal:\n  backend: local\n  cwd: /home/task/workspace\n')
os.environ['PROBE_MODEL_KEY']='fake-local-only'
base=['hermes','chat','-Q','--toolsets','terminal','-q']
def run(label,args):
 before=time.monotonic()
 try: p=subprocess.run(args,capture_output=True,text=True,timeout=75)
 except subprocess.TimeoutExpired as e:
  print(json.dumps({'label':label,'timeout':True,'stdout':str(e.stdout)[-1500:],'stderr':str(e.stderr)[-2500:]}),flush=True); raise
 print(json.dumps({'label':label,'seconds':round(time.monotonic()-before,3),'code':p.returncode,'stdout':p.stdout[-2500:],'stderr':p.stderr[-3500:]}),flush=True)
 return p
if phase=='first':
 p=run('first',base+['remember the first marker'])
 assert p.returncode==0 and 'FIRST_STORED_MARKER' in p.stdout
 ids=re.findall(r'session_id:\s*([A-Za-z0-9_.-]+)',p.stderr); assert ids
 sid=ids[-1]; (home/'probe-session').write_text(sid)
elif phase=='compression':
 # Seed an oversized conversation through the pinned harness's own store API.
 # The CLI, compressor, summary call and session split remain real.
 from hermes_state import SessionDB
 db=SessionDB(); sid='compression-seed'
 db.create_session(sid,'cli',model='fake-model')
 payload=' '.join('context%05d detail value'%i for i in range(1500))
 for n in range(20):
  db.append_message(sid,'user','Earlier question %d '%n+payload)
  db.append_message(sid,'assistant','FIRST_STORED_MARKER earlier answer %d '%n+payload)
 db.end_session(sid,'cli_close'); db.close()
 p=run('compression',base+['Continue using your compressed prior history','--resume',sid])
 assert p.returncode==0 and 'COMPRESSED_HISTORY_PRESENT' in p.stdout
 ids=re.findall(r'session_id:\s*([A-Za-z0-9_.-]+)',p.stderr);assert ids
 sid=ids[-1];assert sid!='compression-seed'
 db=sqlite3.connect('file:'+str(hh/'state.db')+'?mode=ro',uri=True)
 parent=db.execute('SELECT parent_session_id FROM sessions WHERE id=?',(sid,)).fetchone()[0];assert parent
 print(json.dumps({'event':'compression-descendant','session':sid,'parent':parent}),flush=True);db.close()
 (home/'probe-session').write_text(sid)
elif phase=='compressed-resume':
 sid=(home/'probe-session').read_text()
 p=run('compressed-resume',base+['Recall the compressed history','--resume',sid])
 assert p.returncode==0 and 'COMPRESSED_HISTORY_PRESENT' in p.stdout
 assert sid in p.stderr
else:
 sid=(home/'probe-session').read_text()
 p=run('resume',base+['report whether prior assistant history is present','--resume',sid])
 assert p.returncode==0 and 'HISTORY_PRESENT' in p.stdout
 assert sid in p.stderr
if sid:
 db=sqlite3.connect('file:'+str(hh/'state.db')+'?mode=ro',uri=True)
 print(json.dumps({'event':'storage','session':sid,'tables':db.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall(),'messages':db.execute('SELECT COUNT(*) FROM messages WHERE session_id=?',(sid,)).fetchone()[0],'journal':db.execute('PRAGMA journal_mode').fetchone()[0]}),flush=True)
 assert db.execute('SELECT COUNT(*) FROM messages WHERE session_id=?',(sid,)).fetchone()[0]>=2
 db.close()
if phase=='resume':
 p=run('unknown',base+['unknown session must not become fresh','--resume','nonexistent-394-session']); assert p.returncode!=0
 p=run('native',base+['EXERCISE_NATIVE_TERMINAL']); assert p.returncode==0
 print(json.dumps({'event':'native-proof','exists':(home/'workspace/terminal-proof').exists()}),flush=True)
 assert (home/'workspace/terminal-proof').read_text()=='terminal-proof'
print(json.dumps({'event':'summary','requests':len(requests),'uid':os.getuid()}),flush=True)
