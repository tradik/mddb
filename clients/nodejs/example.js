#!/usr/bin/env node
/**
 * MDDB Node.js Client Example
 * 
 * This example demonstrates how to use the MDDB gRPC client in Node.js.
 */

const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');

// Load proto file
const PROTO_PATH = path.join(__dirname, 'proto', 'mddb.proto');
const packageDefinition = protoLoader.loadSync(PROTO_PATH, {
  keepCase: true,
  longs: String,
  enums: String,
  defaults: true,
  oneofs: true
});

const mddb = grpc.loadPackageDefinition(packageDefinition).mddb;

// Create client
const client = new mddb.MDDB('localhost:11024', 
  grpc.credentials.createInsecure());

console.log('🔗 Connected to MDDB server\n');

// ============================================================================
// Add a document
// ============================================================================
console.log('📝 Adding a document...');
client.Add({
  collection: 'blog',
  key: 'nodejs-example',
  lang: 'en_US',
  meta: {
    category: { values: ['tutorial', 'nodejs'] },
    author: { values: ['Node.js Developer'] },
    tags: { values: ['grpc', 'api', 'example', 'javascript'] }
  },
  content_md: '# Node.js gRPC Example\n\nThis document was created using the Node.js client!'
}, (err, doc) => {
  if (err) {
    console.error('❌ Error adding document:', err.message);
    return;
  }
  
  console.log(`✅ Document added: ${doc.id}`);
  console.log(`   Added at: ${doc.added_at}\n`);

  // ==========================================================================
  // Get the document
  // ==========================================================================
  console.log('📖 Retrieving document...');
  client.Get({
    collection: 'blog',
    key: 'nodejs-example',
    lang: 'en_US',
    env: { year: '2024', language: 'Node.js' }
  }, (err, doc) => {
    if (err) {
      console.error('❌ Error getting document:', err.message);
      return;
    }
    
    console.log(`✅ Retrieved: ${doc.key}`);
    console.log(`   Content: ${doc.content_md.substring(0, 50)}...\n`);

    // ========================================================================
    // Search documents
    // ========================================================================
    console.log('🔍 Searching for tutorial documents...');
    client.Search({
      collection: 'blog',
      filter_meta: {
        category: { values: ['tutorial'] }
      },
      sort: 'updatedAt',
      asc: false,
      limit: 10
    }, (err, resp) => {
      if (err) {
        console.error('❌ Error searching:', err.message);
        return;
      }
      
      console.log(`✅ Found ${resp.documents.length} documents`);
      resp.documents.forEach((doc, i) => {
        console.log(`   ${i + 1}. ${doc.key} (${doc.lang})`);
      });
      console.log();

      // ======================================================================
      // Get server statistics
      // ======================================================================
      console.log('📊 Getting server statistics...');
      client.Stats({}, (err, stats) => {
        if (err) {
          console.error('❌ Error getting stats:', err.message);
          return;
        }
        
        console.log('✅ Server Stats:');
        console.log(`   Database: ${stats.database_path}`);
        console.log(`   Size: ${(stats.database_size / 1024).toFixed(2)} KB`);
        console.log(`   Mode: ${stats.mode}`);
        console.log(`   Total Documents: ${stats.total_documents}`);
        console.log(`   Total Revisions: ${stats.total_revisions}\n`);

        if (stats.collections && stats.collections.length > 0) {
          console.log('   Collections:');
          stats.collections.forEach(coll => {
            console.log(`     • ${coll.name}: ${coll.document_count} docs, ` +
                       `${coll.revision_count} revisions`);
          });
          console.log();
        }

        // ====================================================================
        // Create backup
        // ====================================================================
        console.log('💾 Creating backup...');
        client.Backup({
          to: 'nodejs-backup.db'
        }, (err, backup_resp) => {
          if (err) {
            console.error('❌ Error creating backup:', err.message);
            return;
          }
          
          console.log(`✅ Backup created: ${backup_resp.backup}\n`);
          console.log('✨ All operations completed successfully!');
        });
      });
    });
  });
});
