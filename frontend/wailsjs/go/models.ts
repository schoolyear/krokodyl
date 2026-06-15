export namespace main {
	
	export class FileTransfer {
	    id: string;
	    name: string;
	    files: string[];
	    size: number;
	    progress: number;
	    speed: number;
	    status: string;
	    code?: string;
	    peer?: string;
	    error?: string;
	    paths?: string[];
	    resendable?: boolean;
	    path?: string;
	    peerMachineId?: string;
	    resumeCode?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileTransfer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.files = source["files"];
	        this.size = source["size"];
	        this.progress = source["progress"];
	        this.speed = source["speed"];
	        this.status = source["status"];
	        this.code = source["code"];
	        this.peer = source["peer"];
	        this.error = source["error"];
	        this.paths = source["paths"];
	        this.resendable = source["resendable"];
	        this.path = source["path"];
	        this.peerMachineId = source["peerMachineId"];
	        this.resumeCode = source["resumeCode"];
	    }
	}
	export class NearbyPeer {
	    id: string;
	    name: string;
	    addr: string;
	    addrs?: string[];
	    port: number;
	    machineId: string;
	    kind?: string;
	
	    static createFrom(source: any = {}) {
	        return new NearbyPeer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.addr = source["addr"];
	        this.addrs = source["addrs"];
	        this.port = source["port"];
	        this.machineId = source["machineId"];
	        this.kind = source["kind"];
	    }
	}
	export class NearbyPrefs {
	    visible: boolean;
	    lastPeer: string;
	
	    static createFrom(source: any = {}) {
	        return new NearbyPrefs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.visible = source["visible"];
	        this.lastPeer = source["lastPeer"];
	    }
	}
	export class OfflineGuidance {
	    bluetoothAvailable: boolean;
	    ssid: string;
	    psk: string;
	    hostSteps: string[];
	    joinSteps: string[];
	
	    static createFrom(source: any = {}) {
	        return new OfflineGuidance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bluetoothAvailable = source["bluetoothAvailable"];
	        this.ssid = source["ssid"];
	        this.psk = source["psk"];
	        this.hostSteps = source["hostSteps"];
	        this.joinSteps = source["joinSteps"];
	    }
	}
	export class PhoneReceiveInfo {
	    url: string;
	    qrPng: string;
	
	    static createFrom(source: any = {}) {
	        return new PhoneReceiveInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.qrPng = source["qrPng"];
	    }
	}
	export class ResendOutcome {
	    started: boolean;
	    message: string;
	    needsConfirm?: boolean;
	    peerName?: string;
	    peerAddr?: string;
	
	    static createFrom(source: any = {}) {
	        return new ResendOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.started = source["started"];
	        this.message = source["message"];
	        this.needsConfirm = source["needsConfirm"];
	        this.peerName = source["peerName"];
	        this.peerAddr = source["peerAddr"];
	    }
	}

}

