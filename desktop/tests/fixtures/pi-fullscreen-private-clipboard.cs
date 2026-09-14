// Test-only port of the proven windows-private-pi-paired-homes/PrivateProbe.cs.
// No clipboard API, guards, source rewriting, lab transport, or existing namespace opens.
// Only this throwaway executable changes its process station. The original GUI is untouched.
// Associated station/desktop and lifetime job are reclaimed by executable termination;
// no switch/restore to WinSta0. The outer synchronous caller must reap this executable.
// References: learn.microsoft.com/en-us/windows/win32/
// api/winuser/nf-winuser-createwindowstationw (CWF_CREATE_ONLY, explicit protected DACL)
// api/processthreadsapi/ns-processthreadsapi-startupinfow (explicit lpDesktop)
// winstation/process-connection-to-a-window-station (single inherited station)
// procthread/job-objects (nested jobs, kill-on-close, no breakaway)
using System;
using System.Text;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Security.AccessControl;
using System.IO;
using System.Threading;
public static class PrivateClipboard {
 [StructLayout(LayoutKind.Sequential)] struct SA { public int length; public IntPtr descriptor; public int inherit; }
 [DllImport("user32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern IntPtr CreateWindowStationW(string name,uint flags,uint access,ref SA sa);
 [DllImport("user32.dll",SetLastError=true)] static extern bool SetProcessWindowStation(IntPtr h);
 [DllImport("user32.dll",SetLastError=true)] static extern IntPtr GetProcessWindowStation();
 [DllImport("user32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern IntPtr CreateDesktopW(string name,IntPtr device,IntPtr mode,uint flags,uint access,ref SA sa);
 [DllImport("user32.dll",SetLastError=true)] static extern bool SetThreadDesktop(IntPtr h);
 [DllImport("user32.dll",SetLastError=true)] static extern IntPtr GetThreadDesktop(uint id);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool GetUserObjectInformationW(IntPtr h,int index,StringBuilder value,uint bytes,out uint needed);
 static Stopwatch clock=Stopwatch.StartNew();
 static void Mark(string s) { Console.Error.WriteLine(s+" ms="+clock.ElapsedMilliseconds); }
 static void Check(bool ok,string phase) { if(!ok) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(),phase); }
 static string Name(IntPtr h) { Check(h!=IntPtr.Zero,"NULL_HANDLE"); var b=new StringBuilder(256); uint needed; Check(GetUserObjectInformationW(h,2,b,512,out needed),"GET_NAME"); return b.ToString(); }
 static void Verify(string station,string desktop,string phase) {
  string s=Name(GetProcessWindowStation()),d=Name(GetThreadDesktop(GetCurrentThreadId()));
  if(!String.Equals(s,station,StringComparison.Ordinal)||!String.Equals(d,desktop,StringComparison.Ordinal)||!s.StartsWith("VCPrivate-",StringComparison.Ordinal)) throw new Exception("ISOLATION_REFUSED");
  Mark("VERIFIED_"+phase);
 }
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern IntPtr CreateJobObjectW(IntPtr sa,string name);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool SetInformationJobObject(IntPtr job,int info,IntPtr data,uint length);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool AssignProcessToJobObject(IntPtr job,IntPtr process);
 static IntPtr lifetimeJob;
 static void OwnLifetime() {
  // A new unnamed job only. Parent and descendants die if its sole handle closes.
  // No breakaway flag, no existing job mutation, no executable spawned before assignment.
  if(IntPtr.Size!=8) throw new Exception("X64_REQUIRED");
  lifetimeJob=CreateJobObjectW(IntPtr.Zero,null);
  Check(lifetimeJob!=IntPtr.Zero,"CREATE_JOB");
  IntPtr data=Marshal.AllocHGlobal(144);
  try {
   Marshal.Copy(new byte[144],0,data,144);
   Marshal.WriteInt32(data,16,0x2000); // JOBOBJECT_EXTENDED_LIMIT_INFORMATION: KILL_ON_JOB_CLOSE
   Check(SetInformationJobObject(lifetimeJob,9,data,144),"JOB_LIMIT");
   Check(AssignProcessToJobObject(lifetimeJob,Process.GetCurrentProcess().Handle),"JOB_ASSIGN_SELF");
  } finally { Marshal.FreeHGlobal(data); }
  Mark("OWNED_JOB_BOUND");
 }
 [STAThread] public static int Main(string[] args) {
  if(Environment.GetEnvironmentVariable("VC_ISOLATED_CLIPBOARD_ACCEPTANCE")!="I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION") return 90;
  if(args.Length<2 || !Path.IsPathRooted(args[0]) || !Path.IsPathRooted(args[1])) return 90;
  return Run(args);
 }
 static int Run(string[] args) {
  IntPtr station=IntPtr.Zero,desktop=IntPtr.Zero,sd=IntPtr.Zero;
  try {
   if(System.Threading.Thread.CurrentThread.GetApartmentState()!=System.Threading.ApartmentState.STA) throw new Exception("NOT_STA");
   OwnLifetime();
   string s="VCPrivate-"+Guid.NewGuid().ToString("D"),d="VCDesk-"+Guid.NewGuid().ToString("D");

   // Protected DACL: this user's SID only. No privilege adjustment or external object mutation.
   var security=new RawSecurityDescriptor("D:P(A;;GA;;;"+WindowsIdentity.GetCurrent().User.Value+")");
   byte[] bytes=new byte[security.BinaryLength]; security.GetBinaryForm(bytes,0);
   sd=Marshal.AllocHGlobal(bytes.Length); Marshal.Copy(bytes,0,sd,bytes.Length);
   SA sa=new SA {length=Marshal.SizeOf(typeof(SA)),descriptor=sd,inherit=1};
   Mark("CREATE_STATION_BEGIN");
   station=CreateWindowStationW(s,1,0x000F037F,ref sa); // CWF_CREATE_ONLY; WINSTA_ALL_ACCESS
   Check(station!=IntPtr.Zero,"CREATE_STATION");
   Check(SetProcessWindowStation(station),"SET_STATION");
   if(Name(GetProcessWindowStation())!=s) throw new Exception("STATION_REFUSED");

   SA desktopSa=new SA {length=Marshal.SizeOf(typeof(SA)),descriptor=sd,inherit=1};
   desktop=CreateDesktopW(d,IntPtr.Zero,IntPtr.Zero,0,0x000F01FF,ref desktopSa);
   Check(desktop!=IntPtr.Zero,"CREATE_DESKTOP");
   var state=new WorkerState();
   var worker=new System.Threading.Thread(delegate() {
    try {
     // First UI operation on this fresh thread: bind, with no windows/hooks created here.
     Check(SetThreadDesktop(desktop),"SET_DESKTOP");
     Verify(s,d,"BOUND");
     if(System.Threading.Thread.CurrentThread.GetApartmentState()!=System.Threading.ApartmentState.STA) throw new Exception("WORKER_NOT_STA");
     RunChild(s,d,state,args);
    } catch(Exception e) { state.Error=e; }
   });
   worker.SetApartmentState(System.Threading.ApartmentState.STA);
   worker.IsBackground=true;
   Mark("FRESH_STA_START");
   worker.Start();
   if(!worker.Join(55000)) { Mark("FRESH_STA_JOIN_TIMEOUT"); return 93; }
   Mark("FRESH_STA_JOINED");
   if(state.Error!=null) throw state.Error;
   return state.Result;
  } catch(Exception e) {
   var w=e as System.ComponentModel.Win32Exception;
   Mark("REFUSED_OR_FAILED type="+e.GetType().Name+(w==null?"":" win32="+w.NativeErrorCode));
   return 91;
  } finally {
   if(sd!=IntPtr.Zero) Marshal.FreeHGlobal(sd);
   // Keep both owned handles and process association until termination; never restore.
   Mark("CLEANUP_PROCESS_RECLAIM_NO_RESTORE");
  }
 }
 sealed class WorkerState { public int Result=94; public Exception Error; }
 [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)] struct SI { public int cb; public string reserved,desktop,title; public uint x,y,xs,ys,xc,yc,fill,flags; public ushort show,reserved2; public IntPtr reservedPtr,input,output,error; }
 [StructLayout(LayoutKind.Sequential)] struct PI { public IntPtr process,thread; public uint pid,tid; }
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool CreateProcessW(string app,StringBuilder command,IntPtr pa,IntPtr ta,bool inherit,uint flags,IntPtr env,string cwd,ref SI si,out PI pi);
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern IntPtr CreateFileW(string path,uint access,uint share,ref SA sa,uint disposition,uint flags,IntPtr template);
 [DllImport("kernel32.dll",SetLastError=true)] static extern uint ResumeThread(IntPtr thread);
 [DllImport("kernel32.dll",SetLastError=true)] static extern uint WaitForSingleObject(IntPtr handle,uint ms);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool TerminateProcess(IntPtr process,uint code);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool GetExitCodeProcess(IntPtr process,out uint code);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool CloseHandle(IntPtr handle);

 // CommandLineToArgvW/CRT quoting, including empty arguments and trailing backslashes.
 static string Quote(string value) {
  var b=new StringBuilder("\""); int slashes=0;
  foreach(char c in value) {
   if(c=='\\') { slashes++; continue; }
   b.Append('\\',c=='"'?slashes*2+1:slashes); b.Append(c); slashes=0;
  }
  b.Append('\\',slashes*2); return b.Append('"').ToString();
 }
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool TerminateJobObject(IntPtr job,uint code);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool QueryInformationJobObject(IntPtr job,int info,IntPtr data,uint length,IntPtr returned);
 static void ReapJob(IntPtr job) {
  Check(TerminateJobObject(job,99),"TERMINATE_OWNED_JOB");
  IntPtr data=Marshal.AllocHGlobal(48);
  try {
   var deadline=Stopwatch.StartNew();
   do {
    Check(QueryInformationJobObject(job,1,data,48,IntPtr.Zero),"QUERY_OWNED_JOB");
    if(Marshal.ReadInt32(data,40)==0) return; // ActiveProcesses
    Thread.Sleep(20);
   } while(deadline.ElapsedMilliseconds<3000);
   throw new Exception("OWNED_JOB_REAP_TIMEOUT");
  } finally { Marshal.FreeHGlobal(data); }
 }
 static bool TooLarge(string file) { return new FileInfo(file).Length>2*1024*1024; }
 static void Emit(string file,bool stderr) {
  using(var stream=File.OpenRead(file)) {
   byte[] bytes=new byte[2*1024*1024]; int n=0,got;
   while(n<bytes.Length && (got=stream.Read(bytes,n,bytes.Length-n))>0) n+=got;
   (stderr?Console.Error:Console.Out).Write(Encoding.UTF8.GetString(bytes,0,n));
  }
 }
 static void RunChild(string s,string d,WorkerState state,string[] args) {
  Verify(s,d,"LAUNCH");
  Environment.SetEnvironmentVariable("VC_R8_PRIVATE_LAUNCHER","VERIFIED");
  string root=Environment.CurrentDirectory;
  string stdout=Path.Combine(root,"private.stdout"),stderr=Path.Combine(root,"private.stderr");
  PI pi=new PI(); bool created=false,reaped=false;
  IntPtr input=IntPtr.Zero,output=IntPtr.Zero,error=IntPtr.Zero,job=IntPtr.Zero;
  try {
   job=CreateJobObjectW(IntPtr.Zero,null); Check(job!=IntPtr.Zero,"CHILD_JOB");
   SA sa=new SA{length=Marshal.SizeOf(typeof(SA)),inherit=1};
   input=CreateFileW("NUL",0x80000000,3,ref sa,3,0,IntPtr.Zero);
   output=CreateFileW(stdout,0x40000000,3,ref sa,1,0,IntPtr.Zero);
   error=CreateFileW(stderr,0x40000000,3,ref sa,1,0,IntPtr.Zero);
   Check(input!=new IntPtr(-1)&&output!=new IntPtr(-1)&&error!=new IntPtr(-1),"OWNED_IO");
   SI si=new SI{cb=Marshal.SizeOf(typeof(SI)),desktop=s+"\\"+d,flags=0x100,input=input,output=output,error=error};
   var command=new StringBuilder();
   foreach(string arg in args) { if(command.Length>0) command.Append(' '); command.Append(Quote(arg)); }
   // Exact supplied Node/entry/argv. NULL environment inherits the caller's sparse block.
   // Suspended Node's optional GetThreadDesktop witness is deliberately not used:
   // fresh STA creator proof + explicit startup + trusted no-switch code are authority.
   Check(CreateProcessW(args[0],command,IntPtr.Zero,IntPtr.Zero,true,0x08000404,IntPtr.Zero,root,ref si,out pi),"CREATE_NODE"); created=true;
   Check(AssignProcessToJobObject(job,pi.process),"ASSIGN_CHILD_JOB");
   Check(ResumeThread(pi.thread)!=0xffffffff,"RESUME_NODE");
   var deadline=Stopwatch.StartNew();
   while(true) {
    uint wait=WaitForSingleObject(pi.process,50);
    if(TooLarge(stdout)||TooLarge(stderr)) throw new Exception("OUTPUT_LIMIT");
    if(wait==0) break;
    Check(wait==258,"WAIT_NODE");
    if(deadline.ElapsedMilliseconds>=45000) throw new Exception("NODE_TIMEOUT");
   }
   uint code; Check(GetExitCodeProcess(pi.process,out code),"NODE_EXIT_CODE");
   ReapJob(job); reaped=true;
   // Close our write handles AFTER child/job reap, BEFORE any read (File.OpenRead sharing).
   Check(CloseHandle(output),"CLOSE_STDOUT"); output=IntPtr.Zero;
   Check(CloseHandle(error),"CLOSE_STDERR"); error=IntPtr.Zero;
   Emit(stderr,true); Emit(stdout,false);
   state.Result=unchecked((int)code);
  } finally {
   // An assignment failure leaves a suspended child outside the child job, but inside lifetimeJob.
   try {
    if(created&&!reaped) {
     try {
      if(WaitForSingleObject(pi.process,0)!=0) Check(TerminateProcess(pi.process,99),"KILL_NODE");
      Check(WaitForSingleObject(pi.process,2000)==0,"REAP_NODE");
     } finally { ReapJob(job); }
    }
   } finally {
    bool closed=true;
    foreach(IntPtr h in new IntPtr[]{pi.thread,pi.process,input,output,error,job})
     if(h!=IntPtr.Zero&&h!=new IntPtr(-1)) closed=CloseHandle(h)&&closed;
    if(!closed) throw new Exception("CLOSE_OWNED_HANDLE");
   }
  }
 }
}
