package lsd.mapreduce;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.conf.Configured;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.io.*;
import org.apache.hadoop.mapreduce.*;
import org.apache.hadoop.mapreduce.lib.input.*;
import org.apache.hadoop.mapreduce.lib.output.*;
import org.apache.hadoop.util.Tool;
import org.apache.hadoop.util.ToolRunner;

import java.io.IOException;
import java.util.*;
import java.util.regex.Pattern;

/**
 * LSD Inverted Index Builder — MapReduce Job
 *
 * <p>Builds a full-text inverted index from HDFS text records and writes the
 * output to HDFS in tab-delimited format:
 *
 * <pre>
 *   token\tentity_id_1,entity_id_2,...\tfrequency
 * </pre>
 *
 * <p>This output is then loaded into ClickHouse via a Go importer job
 * (feeding the search_token_entity and search_token_bitmap tables).
 *
 * <p>Build:
 *   mvn package -f hadoop/pom.xml
 *   hadoop jar target/lsd-mapreduce-1.0.jar lsd.mapreduce.InvertedIndexJob \
 *     -Dinput=/lsd/processed/token_index_raw \
 *     -Doutput=/lsd/processed/inverted_index
 *
 * <p>Or run via the provided script:
 *   ./hadoop/run-mapreduce.sh
 */
public class InvertedIndexJob extends Configured implements Tool {

    // ── Mapper ─────────────────────────────────────────────────────────────────
    /**
     * Input:  Tab-separated lines from Pig output: token \t [entity_ids] \t frequency
     *         OR raw text lines: entity_id \t full_text
     * Output: (token, entity_id_as_text)
     */
    public static class InvertedIndexMapper
            extends Mapper<LongWritable, Text, Text, Text> {

        private static final Pattern WHITESPACE = Pattern.compile("[\\s.,;:@\\-_!?\"'()\\[\\]{}]+");
        private static final Pattern STOPWORDS  = Pattern.compile(
                "^(the|and|or|is|in|at|of|to|a|an|for|it|on|with|as|are|was|has|have|be)$");
        private static final int MIN_TOKEN_LENGTH = 2;
        private static final int MAX_TOKEN_LENGTH = 50;

        private final Text outKey   = new Text();
        private final Text outValue = new Text();

        @Override
        protected void map(LongWritable key, Text value, Context context)
                throws IOException, InterruptedException {

            String line = value.toString().trim();
            if (line.isEmpty() || line.startsWith("#")) {
                return;
            }

            String[] parts = line.split("\t", 2);
            if (parts.length < 2) {
                return;
            }

            String entityIdStr  = parts[0].trim();
            String searchText   = parts[1].trim().toLowerCase(java.util.Locale.ROOT);

            // Validate entity ID is numeric
            if (!entityIdStr.matches("\\d+")) {
                return;
            }

            // Tokenize
            String[] tokens = WHITESPACE.split(searchText);
            Set<String> seen = new HashSet<>();

            for (String token : tokens) {
                token = token.trim();
                if (token.length() < MIN_TOKEN_LENGTH
                        || token.length() > MAX_TOKEN_LENGTH
                        || STOPWORDS.matcher(token).matches()
                        || token.matches("\\d+")       // pure numbers — skip
                        || !seen.add(token)) {         // deduplicate per record
                    continue;
                }

                outKey.set(token);
                outValue.set(entityIdStr);
                context.write(outKey, outValue);
            }

            context.getCounter("LSD", "RecordsProcessed").increment(1L);
        }
    }

    // ── Combiner ───────────────────────────────────────────────────────────────
    /**
     * Local pre-aggregation: collects entity IDs per token on each mapper node
     * before shuffle — dramatically reduces network traffic.
     */
    public static class InvertedIndexCombiner
            extends Reducer<Text, Text, Text, Text> {

        private final Text outValue = new Text();

        @Override
        protected void reduce(Text token, Iterable<Text> entityIds, Context context)
                throws IOException, InterruptedException {

            Set<String> uniqueIds = new LinkedHashSet<>();
            for (Text id : entityIds) {
                uniqueIds.add(id.toString());
                if (uniqueIds.size() >= 10_000) {
                    break; // safety limit per combiner
                }
            }
            outValue.set(String.join(",", uniqueIds));
            context.write(token, outValue);
        }
    }

    // ── Reducer ────────────────────────────────────────────────────────────────
    /**
     * Input:  (token, [entity_id_csv, entity_id_csv, ...])
     * Output: token \t entity_id_1,entity_id_2,... \t total_frequency
     *
     * Final output file is loaded into ClickHouse search_token_entity table
     * by the Go post-processing job (internal/hadoop/clickhouse_loader.go).
     */
    public static class InvertedIndexReducer
            extends Reducer<Text, Text, Text, Text> {

        private static final int MAX_ENTITY_IDS_PER_TOKEN = 1_000_000;
        private final Text outValue = new Text();

        @Override
        protected void reduce(Text token, Iterable<Text> values, Context context)
                throws IOException, InterruptedException {

            Set<String> entityIds = new LinkedHashSet<>();

            for (Text val : values) {
                String[] ids = val.toString().split(",");
                for (String id : ids) {
                    id = id.trim();
                    if (!id.isEmpty()) {
                        entityIds.add(id);
                    }
                    if (entityIds.size() >= MAX_ENTITY_IDS_PER_TOKEN) {
                        context.getCounter("LSD", "TruncatedTokens").increment(1L);
                        break;
                    }
                }
                if (entityIds.size() >= MAX_ENTITY_IDS_PER_TOKEN) break;
            }

            if (entityIds.isEmpty()) return;

            // Format: entity_id_list \t frequency
            outValue.set(String.join(",", entityIds) + "\t" + entityIds.size());
            context.write(token, outValue);

            context.getCounter("LSD", "UniqueTokens").increment(1L);
        }
    }

    // ── Partitioner ────────────────────────────────────────────────────────────
    /**
     * Routes tokens to reducers by first character to create alphabetically
     * ordered partitions — makes ClickHouse bulk loading more predictable.
     */
    public static class AlphaPartitioner extends Partitioner<Text, Text> {
        @Override
        public int getPartition(Text key, Text value, int numPartitions) {
            char first = key.getLength() > 0 ? key.toString().charAt(0) : '0';
            if (first >= 'a' && first <= 'z') {
                return (first - 'a') % numPartitions;
            }
            return (numPartitions - 1); // digits and symbols → last partition
        }
    }

    // ── Driver ─────────────────────────────────────────────────────────────────
    @Override
    public int run(String[] args) throws Exception {
        Configuration conf = getConf();

        String inputPath  = conf.get("input",  "/lsd/processed/token_index_raw");
        String outputPath = conf.get("output", "/lsd/processed/inverted_index");
        int numReducers   = Integer.parseInt(conf.get("reducers", "26")); // 26 partitions (a-z + misc)

        Job job = Job.getInstance(conf, "LSD Inverted Index Builder");
        job.setJarByClass(InvertedIndexJob.class);

        // I/O
        FileInputFormat.addInputPath(job,  new Path(inputPath));
        FileOutputFormat.setOutputPath(job, new Path(outputPath));
        job.setInputFormatClass(TextInputFormat.class);
        job.setOutputFormatClass(TextOutputFormat.class);

        // Classes
        job.setMapperClass(InvertedIndexMapper.class);
        job.setCombinerClass(InvertedIndexCombiner.class);
        job.setReducerClass(InvertedIndexReducer.class);
        job.setPartitionerClass(AlphaPartitioner.class);

        // Output types
        job.setMapOutputKeyClass(Text.class);
        job.setMapOutputValueClass(Text.class);
        job.setOutputKeyClass(Text.class);
        job.setOutputValueClass(Text.class);

        // Performance tuning
        job.setNumReduceTasks(numReducers);
        conf.set("mapreduce.job.reduces",           String.valueOf(numReducers));
        conf.set("mapreduce.map.memory.mb",         "2048");
        conf.set("mapreduce.reduce.memory.mb",      "4096");
        conf.set("mapreduce.map.java.opts",         "-Xmx1638m");
        conf.set("mapreduce.reduce.java.opts",      "-Xmx3276m");
        conf.set("mapreduce.task.io.sort.mb",       "512");
        conf.set("mapreduce.task.io.sort.factor",   "100");
        conf.set("mapreduce.reduce.shuffle.parallelcopies", "20");

        // Compression for shuffle
        conf.setBoolean("mapreduce.map.output.compress", true);
        conf.set("mapreduce.map.output.compress.codec",
                "org.apache.hadoop.io.compress.SnappyCodec");

        // Output compression
        FileOutputFormat.setCompressOutput(job, true);
        FileOutputFormat.setOutputCompressorClass(job, org.apache.hadoop.io.compress.GzipCodec.class);

        boolean success = job.waitForCompletion(true);

        // Print counters summary
        Counters counters = job.getCounters();
        CounterGroup group = counters.getGroup("LSD");
        System.out.println("═══════════════════════════════════════════");
        System.out.println("LSD MapReduce Job Summary");
        System.out.printf("  Records processed : %,d%n", group.findCounter("RecordsProcessed").getValue());
        System.out.printf("  Unique tokens     : %,d%n", group.findCounter("UniqueTokens").getValue());
        System.out.printf("  Truncated tokens  : %,d%n", group.findCounter("TruncatedTokens").getValue());
        System.out.println("  Output → " + outputPath);
        System.out.println("═══════════════════════════════════════════");

        return success ? 0 : 1;
    }

    public static void main(String[] args) throws Exception {
        int exitCode = ToolRunner.run(new Configuration(), new InvertedIndexJob(), args);
        System.exit(exitCode);
    }
}
