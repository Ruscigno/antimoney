const fs = require('fs');
const readline = require('readline');
const crypto = require('crypto');

// Generate deterministic UUIDv4 from a string for repeatable mapping
function uuid(str) {
    const hash = crypto.createHash('md5').update(String(str)).digest('hex');
    return [
        hash.substr(0, 8),
        hash.substr(8, 4),
        '4' + hash.substr(13, 3),
        '8' + hash.substr(17, 3), // could be 8,9,a,b
        hash.substr(20, 12)
    ].join('-');
}

async function run() {
    const accountsCSV = './accounts.csv';
    const txsCSV = './transactions.csv';
    const jsonFile = './import.json';

    const outputData = {
        accounts: [],
        transactions: []
    };

    const cad_guid = uuid('cad-currency-guid'); // Will just need some GUID for CAD. Wait, the backend currently requires CAD to be in the commodities table, but the import handler takes whatever commodity_guid we give it. We will use a predictable GUID for CAD, or we might need the backend to insert CAD if it doesn't exist, but our current backend seed does this. For now let's just use a predictable one.

    // Let's use a very specific deterministic guid for CAD that matches the backend or we can hardcode the one the backend generated.
    // Actually, backend SeedDatabase creates a CAD commodity with a random uuid if it doesn't exist.
    // But since this JSON will be imported, we'll need commodity_guid to be something the DB knows.
    // Actually, in NewImportExportHandler, I removed the logic that ensures the CAD commodity exists.
    // Wait, the backend handlers/import_export.go just inserts whatever commodity_guid we pass in. So this will work as long as it matches the DB's CAD guid, OR if it references an existing one. That might be a limitation.
    // Let's use a random static guid for CAD and rely on the database having it, or modify backend to auto-create CAD if missing. Let's make the backend handle it or assume the user already has CAD since it's default.

    // We need to map GnuCash full account name to guid, and parent guid
    const accounts = [];
    const accountsInStream = readline.createInterface({
        input: fs.createReadStream(accountsCSV),
        crlfDelay: Infinity
    });

    let isHeader = true;
    for await (const line of accountsInStream) {
        if (isHeader) { isHeader = false; continue; }
        if (!line.trim()) continue;

        // Parse CSV with quotes
        const cols = [];
        let cur = '';
        let inQuote = false;
        for (let i = 0; i < line.length; i++) {
            if (line[i] === '"' && (i === 0 || line[i - 1] !== '\\')) {
                inQuote = !inQuote;
            } else if (line[i] === ',' && !inQuote) {
                cols.push(cur);
                cur = '';
            } else {
                cur += line[i];
            }
        }
        cols.push(cur);

        const [type, fullName, accName, code, desc, color, notes, symbol, ns, hidden, tax, placeholder] = cols.map(c => c.replace(/^"|"$/g, ''));

        // Find parent
        let parentName = null;
        const pts = fullName.split(':');
        if (pts.length > 1) {
            parentName = pts.slice(0, pts.length - 1).join(':');
        }

        accounts.push({
            guid: uuid('acc-' + fullName),
            parent_guid: parentName ? uuid('acc-' + parentName) : null,
            name: accName,
            fullName: fullName,
            type: type === 'BANK' ? 'BANK' : type,
            placeholder: placeholder === 'T',
            desc: (desc || '')
        });
    }

    accounts.sort((a, b) => a.fullName.split(':').length - b.fullName.split(':').length);

    for (const acc of accounts) {
        let typ = acc.type;
        if (typ === 'CREDIT') typ = 'CREDIT';

        outputData.accounts.push({
            guid: acc.guid,
            name: acc.name,
            account_type: typ,
            parent_guid: acc.parent_guid,
            placeholder: acc.placeholder,
            description: acc.desc
        });
    }

    const txStream = readline.createInterface({
        input: fs.createReadStream(txsCSV),
        crlfDelay: Infinity
    });

    isHeader = true;
    let currentTxId = null;
    let splits = [];
    let lastDt = '';
    let lastNum = '';
    let lastDesc = '';

    const txs = [];

    const flushTx = () => {
        if (currentTxId && splits.length > 0) {
            txs.push({ id: uuid('tx-' + currentTxId), originalId: currentTxId, date: lastDt, num: lastNum, desc: lastDesc, splits });
            splits = [];
        }
    };

    for await (const line of txStream) {
        if (isHeader) { isHeader = false; continue; }
        if (!line.trim()) continue;

        const cols = [];
        let cur = '';
        let inQuote = false;
        for (let i = 0; i < line.length; i++) {
            if (line[i] === '"' && (i === 0 || line[i - 1] !== '\\')) {
                inQuote = !inQuote;
            } else if (line[i] === ',' && !inQuote) {
                cols.push(cur);
                cur = '';
            } else {
                cur += line[i];
            }
        }
        cols.push(cur);

        const [date, txid, num, desc, notes, curr, f7, f8, memo, fAccName, accName, symAmt, numAmt, symVal, numVal, rec, recDt] = cols.map(c => c.replace(/^"|"$/g, ''));

        if (txid !== currentTxId) {
            flushTx();
            currentTxId = txid;
            lastDt = date;
            lastNum = num;
            lastDesc = desc;
        }

        const amt = parseFloat(numAmt);
        if (isNaN(amt)) continue;

        splits.push({
            guid: uuid('spt-' + currentTxId + '-' + splits.length),
            accName: fAccName,
            memo: memo || '',
            numAmt: Math.round(amt * 100),
            rec
        });
    }
    flushTx();

    for (const t of txs) {
        const txObj = {
            guid: t.id,
            custom_id: t.num || '',
            post_date: t.date + ' 12:00:00',
            enter_date: new Date().toISOString().slice(0, 19).replace('T', ' '),
            description: t.desc,
            splits: []
        };
        for (const s of t.splits) {
            txObj.splits.push({
                guid: s.guid,
                account_guid: uuid('acc-' + s.accName),
                memo: s.memo,
                value_num: s.numAmt,
                value_denom: 100,
                quantity_num: s.numAmt,
                quantity_denom: 100,
                reconcile_state: s.rec === 'y' ? 'y' : 'n'
            });
        }
        outputData.transactions.push(txObj);
    }

    fs.writeFileSync(jsonFile, JSON.stringify(outputData, null, 2));
    console.log("Done generating import.json");
}

run().catch(console.error);
